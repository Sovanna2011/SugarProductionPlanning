package postgres

import "context"

// PlannedScopeCodes returns the set of storage location and storage group codes
// that have at least one daily storage plan line.
//
// The dashboard uses it to decide which cards can offer a forward projection:
// in the 2026/27 plan the raw sugar and finished sugar warehouses are planned
// as pools, so the individual warehouses have no curve of their own.
func (s *Store) PlannedScopeCodes(ctx context.Context, factoryID int64) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT COALESCE(l.storage_code, g.group_code)
		FROM daily_storage_plans d
		LEFT JOIN storage_locations l ON l.id = d.storage_location_id
		LEFT JOIN storage_groups g    ON g.id = d.storage_group_id
		WHERE ($1 = 0 OR d.factory_id = $1)`, factoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		out[code] = true
	}
	return out, rows.Err()
}
