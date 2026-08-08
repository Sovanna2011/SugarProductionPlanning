// Package httpapi exposes the planning system over HTTP/JSON.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/auth"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/service"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/store/postgres"
)

// API wires the service into an http.Handler.
type API struct {
	svc    *service.Service
	log    *slog.Logger
	webDir string
	auth   auth.Config
}

// New builds the API. webDir, when set, is served at / so the UI5 dashboard and
// the backend can run from one process in development.
func New(svc *service.Service, log *slog.Logger, webDir string, authCfg auth.Config) *API {
	return &API{svc: svc, log: log, webDir: webDir, auth: authCfg}
}

// Routes returns the configured mux.
func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health", a.health)

	// Master data.
	mux.HandleFunc("GET /api/v1/factories", a.factories)
	mux.HandleFunc("GET /api/v1/master/storage-types", a.storageTypes)
	mux.HandleFunc("GET /api/v1/master/storage-locations", a.storageLocations)
	mux.HandleFunc("GET /api/v1/master/storage-groups", a.storageGroups)
	mux.HandleFunc("GET /api/v1/master/products", a.products)
	mux.HandleFunc("GET /api/v1/master/packaging-types", a.packagingTypes)
	mux.HandleFunc("GET /api/v1/master/product-packaging", a.productPackaging)
	mux.HandleFunc("GET /api/v1/master/storage-capacities", a.storageCapacities)
	mux.HandleFunc("GET /api/v1/master/threshold-levels", a.thresholdLevels)
	mux.HandleFunc("GET /api/v1/master/movement-types", a.movementTypes)

	// Master data maintenance. Everything the requirement calls configurable
	// is maintained here rather than by editing SQL.
	mux.HandleFunc("POST /api/v1/master/storage-locations", a.saveStorageLocation)
	mux.HandleFunc("POST /api/v1/master/storage-capacities", a.saveStorageCapacity)
	mux.HandleFunc("POST /api/v1/master/packaging-types", a.savePackagingType)
	mux.HandleFunc("PUT /api/v1/master/threshold-levels", a.replaceThresholds)

	// Inventory.
	mux.HandleFunc("GET /api/v1/inventory/balances", a.balances)
	mux.HandleFunc("GET /api/v1/inventory/movements", a.movements)
	mux.HandleFunc("POST /api/v1/inventory/movements", a.postMovement)
	mux.HandleFunc("POST /api/v1/inventory/movements/validate", a.validateMovement)
	mux.HandleFunc("POST /api/v1/inventory/reservations", a.reserve)
	mux.HandleFunc("POST /api/v1/inventory/reservations/release", a.release)

	// Planning and dashboard.
	mux.HandleFunc("GET /api/v1/seasons", a.seasons)
	mux.HandleFunc("GET /api/v1/planning/production", a.productionPlan)
	mux.HandleFunc("GET /api/v1/planning/storage", a.storagePlan)
	mux.HandleFunc("POST /api/v1/planning/storage", a.saveStoragePlan)
	mux.HandleFunc("GET /api/v1/planning/projection", a.projection)
	mux.HandleFunc("GET /api/v1/planning/plan-vs-actual", a.planVsActual)
	mux.HandleFunc("GET /api/v1/dashboard/storage-capacity", a.dashboard)
	mux.HandleFunc("GET /api/v1/alerts", a.alerts)

	if a.webDir != "" {
		mux.Handle("/", http.FileServer(http.Dir(a.webDir)))
	}

	// Identity is established before anything else, so every handler and
	// service below reads the actor from the request context.
	return a.recoverPanic(cors(auth.Middleware(a.auth, a.log)(mux)))
}

// --- helpers ---------------------------------------------------------------

func (a *API) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		a.log.Error("encode response", "error", err)
	}
}

type errorBody struct {
	Error string `json:"error"`
}

// fail writes an error response.
//
// A 4xx is something the caller can act on — a capacity rule, a bad field, a
// stale version — so its message is returned verbatim; that is the whole point
// of the validation findings. A 5xx is not: the underlying error is usually a
// database failure whose text names tables, columns and constraints, which the
// caller has no use for and should not be handed. Those are logged in full and
// answered generically.
func (a *API) fail(w http.ResponseWriter, r *http.Request, status int, err error) {
	if status >= http.StatusInternalServerError {
		a.log.Error("request failed", "path", r.URL.Path, "status", status, "error", err)
		a.writeJSON(w, status, errorBody{Error: "internal error"})
		return
	}
	a.log.Warn("request rejected", "path", r.URL.Path, "status", status, "error", err)
	a.writeJSON(w, status, errorBody{Error: err.Error()})
}

// handle turns a function returning (payload, error) into a handler, mapping a
// validation error to 400 and anything else to 500.
func (a *API) handle(w http.ResponseWriter, r *http.Request, fn func() (any, error)) {
	body, err := fn()
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, service.ErrValidation):
			status = http.StatusBadRequest
		case errors.Is(err, service.ErrNotFound):
			status = http.StatusNotFound
		case errors.Is(err, service.ErrConflict):
			status = http.StatusConflict
		case errors.Is(err, service.ErrForbidden):
			status = http.StatusForbidden
		}
		a.fail(w, r, status, err)
		return
	}
	a.writeJSON(w, http.StatusOK, body)
}

func intParam(r *http.Request, name string) int64 {
	v, _ := strconv.ParseInt(r.URL.Query().Get(name), 10, 64)
	return v
}

func dateParam(r *http.Request, name string) time.Time {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				a.log.Error("panic serving request", "path", r.URL.Path, "panic", rec)
				a.writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// --- handlers --------------------------------------------------------------

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

func (a *API) factories(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) { return a.svc.Store().ListFactories(r.Context()) })
}

func (a *API) storageTypes(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) { return a.svc.Store().ListStorageTypes(r.Context()) })
}

func (a *API) storageLocations(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		return a.svc.Store().ListStorageLocations(r.Context(), postgres.StorageLocationFilter{
			FactoryID:       intParam(r, "factoryId"),
			StorageTypeCode: r.URL.Query().Get("storageType"),
			StorageCode:     r.URL.Query().Get("storageCode"),
			IncludeInactive: r.URL.Query().Get("includeInactive") == "true",
		})
	})
}

func (a *API) storageGroups(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		return a.svc.Store().ListStorageGroups(r.Context(), intParam(r, "factoryId"))
	})
}

func (a *API) products(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) { return a.svc.Store().ListProducts(r.Context()) })
}

func (a *API) packagingTypes(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) { return a.svc.Store().ListPackagingTypes(r.Context()) })
}

func (a *API) productPackaging(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) { return a.svc.Store().ListProductPackaging(r.Context()) })
}

func (a *API) storageCapacities(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		return a.svc.Store().ListStorageProductCapacity(r.Context(), postgres.CapacityFilter{
			FactoryID:   intParam(r, "factoryId"),
			StorageCode: r.URL.Query().Get("storageCode"),
			ProductID:   intParam(r, "productId"),
			OnDate:      dateParam(r, "date"),
		})
	})
}

func (a *API) thresholdLevels(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		return a.svc.Store().ListThresholdBands(r.Context(), intParam(r, "factoryId"))
	})
}

func (a *API) movementTypes(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) { return a.svc.Store().ListMovementTypes(r.Context()) })
}

func (a *API) balances(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		return a.svc.Store().ListBalances(r.Context(), postgres.BalanceFilter{
			FactoryID:       intParam(r, "factoryId"),
			StorageCode:     r.URL.Query().Get("storageCode"),
			StorageTypeCode: r.URL.Query().Get("storageType"),
			ProductID:       intParam(r, "productId"),
			BatchNo:         r.URL.Query().Get("batchNo"),
			NonZeroOnly:     r.URL.Query().Get("nonZeroOnly") == "true",
		})
	})
}

func (a *API) movements(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		return a.svc.Store().ListMovements(r.Context(), postgres.MovementFilter{
			FactoryID: intParam(r, "factoryId"),
			From:      dateParam(r, "from"),
			To:        dateParam(r, "to"),
			Limit:     int(intParam(r, "limit")),
		})
	})
}

func (a *API) postMovement(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		req, err := decodeMovement(w, r)
		if err != nil {
			return nil, err
		}
		return a.svc.PostMovement(r.Context(), req)
	})
}

func (a *API) validateMovement(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		req, err := decodeMovement(w, r)
		if err != nil {
			return nil, err
		}
		return a.svc.ValidateMovement(r.Context(), req)
	})
}

// maxBodyBytes caps a request body. The planning endpoints accept arrays of
// plan lines, so the limit is generous but not unbounded.
const maxBodyBytes = 4 << 20

func decodeMovement(w http.ResponseWriter, r *http.Request) (service.MovementRequest, error) {
	return decodeBody[service.MovementRequest](w, r)
}

// decodeBody reads a JSON request body, rejecting unknown fields so a
// misspelled key is reported rather than silently ignored.
//
// The ResponseWriter is handed to MaxBytesReader so an oversized body closes
// the connection properly instead of being read to exhaustion.
func decodeBody[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var body T
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		return body, errors.Join(service.ErrValidation, err)
	}
	return body, nil
}

// --- master data maintenance ------------------------------------------------

func (a *API) saveStorageLocation(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		in, err := decodeBody[service.StorageLocationInput](w, r)
		if err != nil {
			return nil, err
		}
		return a.svc.SaveStorageLocation(r.Context(), in)
	})
}

func (a *API) saveStorageCapacity(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		in, err := decodeBody[service.StorageProductCapacityInput](w, r)
		if err != nil {
			return nil, err
		}
		return a.svc.SaveStorageProductCapacity(r.Context(), in)
	})
}

func (a *API) savePackagingType(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		in, err := decodeBody[service.PackagingTypeInput](w, r)
		if err != nil {
			return nil, err
		}
		return a.svc.SavePackagingType(r.Context(), in)
	})
}

func (a *API) replaceThresholds(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		in, err := decodeBody[service.ThresholdBandsInput](w, r)
		if err != nil {
			return nil, err
		}
		return a.svc.ReplaceThresholdBands(r.Context(), in)
	})
}

// --- reservations -----------------------------------------------------------

func (a *API) reserve(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		in, err := decodeBody[service.ReservationRequest](w, r)
		if err != nil {
			return nil, err
		}
		return a.svc.Reserve(r.Context(), in)
	})
}

func (a *API) release(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		in, err := decodeBody[service.ReservationRequest](w, r)
		if err != nil {
			return nil, err
		}
		return a.svc.Release(r.Context(), in)
	})
}

// --- daily storage plan -----------------------------------------------------

// saveStoragePlan accepts either a single plan line or an array of them.
func (a *API) saveStoragePlan(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err != nil {
			return nil, errors.Join(service.ErrValidation, err)
		}

		var lines []service.StoragePlanLineInput
		if err := json.Unmarshal(raw, &lines); err != nil {
			var single service.StoragePlanLineInput
			if err2 := json.Unmarshal(raw, &single); err2 != nil {
				return nil, errors.Join(service.ErrValidation, err2)
			}
			lines = []service.StoragePlanLineInput{single}
		}
		return a.svc.SaveStoragePlan(r.Context(), lines)
	})
}

func (a *API) seasons(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		return a.svc.Store().ListSeasons(r.Context(), intParam(r, "factoryId"))
	})
}

func (a *API) productionPlan(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		return a.svc.Store().ListDailyProductionPlans(r.Context(), postgres.ProductionPlanFilter{
			FactoryID: intParam(r, "factoryId"),
			SeasonID:  intParam(r, "seasonId"),
			From:      dateParam(r, "from"),
			To:        dateParam(r, "to"),
		})
	})
}

func (a *API) storagePlan(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		return a.svc.Store().ListDailyStoragePlans(r.Context(), postgres.StoragePlanFilter{
			FactoryID: intParam(r, "factoryId"),
			ScopeCode: r.URL.Query().Get("scope"),
			From:      dateParam(r, "from"),
			To:        dateParam(r, "to"),
		})
	})
}

func (a *API) projection(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		scope := r.URL.Query().Get("scope")
		if scope == "" {
			return nil, errors.Join(service.ErrValidation, errors.New("scope is required"))
		}
		return a.svc.Project(r.Context(), service.ProjectionRequest{
			FactoryID:        intParam(r, "factoryId"),
			ScopeCode:        scope,
			From:             dateParam(r, "from"),
			Days:             int(intParam(r, "days")),
			UseActualOpening: r.URL.Query().Get("useActualOpening") == "true",
		})
	})
}

func (a *API) planVsActual(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		date := dateParam(r, "date")
		if date.IsZero() {
			date = time.Now()
		}
		return a.svc.PlanVsActual(r.Context(), intParam(r, "factoryId"), date)
	})
}

func (a *API) dashboard(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		return a.svc.Dashboard(r.Context(), service.DashboardFilter{
			FactoryID:       intParam(r, "factoryId"),
			StorageTypeCode: r.URL.Query().Get("storageType"),
			StorageCode:     r.URL.Query().Get("storageCode"),
			ProductCode:     r.URL.Query().Get("productCode"),
			PackagingCode:   r.URL.Query().Get("packagingCode"),
			Date:            dateParam(r, "date"),
		})
	})
}

func (a *API) alerts(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		d, err := a.svc.Dashboard(r.Context(), service.DashboardFilter{
			FactoryID: intParam(r, "factoryId"),
			Date:      dateParam(r, "date"),
		})
		if err != nil {
			return nil, err
		}
		return d.Alerts, nil
	})
}
