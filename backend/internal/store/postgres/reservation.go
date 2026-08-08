package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/capacity"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/domain"
)

// ErrInsufficientStock means a reservation would commit more than is
// available, or a release would free more than is reserved.
var ErrInsufficientStock = errors.New("insufficient stock")

// ReservationAdjustment moves the reserved quantity on one balance row.
// Deltas are signed: positive reserves, negative releases.
type ReservationAdjustment struct {
	StorageLocationID int64
	ProductID         int64
	PackagingTypeID   int64
	BatchNo           string
	DeltaPackages     float64
	DeltaWeight       float64
}

// AdjustReservation changes the reserved quantity on a balance and returns the
// resulting row.
//
// The whole thing runs in one transaction with the balance row locked, so two
// concurrent reservations cannot both see the same free stock and both take
// it. The CHECK constraints on the table are the final backstop; the explicit
// bounds test here exists to give a useful error rather than a constraint
// violation.
func (s *Store) AdjustReservation(ctx context.Context, in ReservationAdjustment) (capacity.Quantity, domain.InventoryBalance, error) {
	var applied capacity.Quantity
	var balance domain.InventoryBalance

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return applied, balance, err
	}
	defer tx.Rollback(ctx)

	var (
		id                                       int64
		packages, weight, resPackages, resWeight float64
	)
	err = tx.QueryRow(ctx, `
		SELECT id, package_quantity, weight_quantity,
		       reserved_package_quantity, reserved_weight_quantity
		FROM inventory_balances
		WHERE storage_location_id = $1 AND product_id = $2
		  AND packaging_type_id = $3 AND batch_no = $4
		FOR UPDATE`,
		in.StorageLocationID, in.ProductID, in.PackagingTypeID, in.BatchNo).
		Scan(&id, &packages, &weight, &resPackages, &resWeight)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return applied, balance, pgx.ErrNoRows
		}
		return applied, balance, err
	}

	newResPackages := resPackages + in.DeltaPackages
	newResWeight := resWeight + in.DeltaWeight

	if newResWeight < -capacity.Epsilon || newResPackages < -capacity.Epsilon {
		return applied, balance, fmt.Errorf(
			"%w: only %.3f t (%.0f packages) is reserved, which is less than the release requested",
			ErrInsufficientStock, resWeight, resPackages)
	}
	if newResWeight > weight+capacity.Epsilon {
		return applied, balance, fmt.Errorf(
			"%w: %.3f t on hand with %.3f t already reserved, so only %.3f t is available",
			ErrInsufficientStock, weight, resWeight, weight-resWeight)
	}
	if newResPackages > packages+capacity.Epsilon {
		return applied, balance, fmt.Errorf(
			"%w: %.0f packages on hand with %.0f already reserved, so only %.0f are available",
			ErrInsufficientStock, packages, resPackages, packages-resPackages)
	}

	actor, err := s.ActorID(ctx)
	if err != nil {
		return applied, balance, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE inventory_balances SET
			reserved_package_quantity = $2,
			reserved_weight_quantity  = $3,
			changed_by = $4, version = version + 1
		WHERE id = $1`, id, newResPackages, newResWeight, actor); err != nil {
		return applied, balance, fmt.Errorf("update reservation: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return applied, balance, err
	}

	applied = capacity.Quantity{
		Packages: capacity.Round(in.DeltaPackages, 3),
		Weight:   capacity.Round(in.DeltaWeight, 3),
	}
	balance = domain.InventoryBalance{
		ID:                id,
		StorageLocationID: in.StorageLocationID,
		ProductID:         in.ProductID,
		PackagingTypeID:   in.PackagingTypeID,
		BatchNo:           in.BatchNo,
		PackageQty:        packages,
		WeightQty:         weight,
		ReservedPackage:   newResPackages,
		ReservedWeight:    newResWeight,
	}
	return applied, balance, nil
}
