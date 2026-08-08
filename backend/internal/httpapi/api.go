// Package httpapi exposes the planning system over HTTP/JSON.
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/service"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/store/postgres"
)

// API wires the service into an http.Handler.
type API struct {
	svc    *service.Service
	log    *slog.Logger
	webDir string
}

// New builds the API. webDir, when set, is served at / so the UI5 dashboard and
// the backend can run from one process in development.
func New(svc *service.Service, log *slog.Logger, webDir string) *API {
	return &API{svc: svc, log: log, webDir: webDir}
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

	// Inventory.
	mux.HandleFunc("GET /api/v1/inventory/balances", a.balances)
	mux.HandleFunc("GET /api/v1/inventory/movements", a.movements)
	mux.HandleFunc("POST /api/v1/inventory/movements", a.postMovement)
	mux.HandleFunc("POST /api/v1/inventory/movements/validate", a.validateMovement)

	// Planning and dashboard.
	mux.HandleFunc("GET /api/v1/seasons", a.seasons)
	mux.HandleFunc("GET /api/v1/planning/production", a.productionPlan)
	mux.HandleFunc("GET /api/v1/planning/storage", a.storagePlan)
	mux.HandleFunc("GET /api/v1/planning/projection", a.projection)
	mux.HandleFunc("GET /api/v1/planning/plan-vs-actual", a.planVsActual)
	mux.HandleFunc("GET /api/v1/dashboard/storage-capacity", a.dashboard)
	mux.HandleFunc("GET /api/v1/alerts", a.alerts)

	if a.webDir != "" {
		mux.Handle("/", http.FileServer(http.Dir(a.webDir)))
	}

	return a.recoverPanic(cors(mux))
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

func (a *API) fail(w http.ResponseWriter, r *http.Request, status int, err error) {
	a.log.Warn("request failed", "path", r.URL.Path, "status", status, "error", err)
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
		req, err := decodeMovement(r)
		if err != nil {
			return nil, err
		}
		return a.svc.PostMovement(r.Context(), req)
	})
}

func (a *API) validateMovement(w http.ResponseWriter, r *http.Request) {
	a.handle(w, r, func() (any, error) {
		req, err := decodeMovement(r)
		if err != nil {
			return nil, err
		}
		return a.svc.ValidateMovement(r.Context(), req)
	})
}

func decodeMovement(r *http.Request) (service.MovementRequest, error) {
	var req service.MovementRequest
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, errors.Join(service.ErrValidation, err)
	}
	return req, nil
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
