package handler

import (
	"errors"
	"net/http"

	"github.com/zhenjb/ganc-sys/internal/request"
	"github.com/zhenjb/ganc-sys/internal/response"
	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// OrderHandler exposes the P4 order/orderbook API (INT-T01).
//
// It depends on the service.OrderService INTERFACE, not a concrete type, so the
// same routes serve the INT-T01 mock and the INT-T02..T04 real implementation
// with no route/handler churn.
//
// INT-T01 status (mock):
//   - GET  /api/markets            → static market registry
//   - POST /api/order              → echoes the order as "open"
//   - GET  /api/orderbook/{market} → static depth snapshot
type OrderHandler struct {
	orderService service.OrderService
}

func NewOrderHandler(orderService service.OrderService) *OrderHandler {
	return &OrderHandler{
		orderService: orderService,
	}
}

// ListMarkets handles GET /api/markets.
func (h *OrderHandler) ListMarkets(w http.ResponseWriter, r *http.Request) {
	result := h.orderService.ListMarkets(r.Context())

	response.JSON(w, http.StatusOK, result)
}

// CreateOrder handles POST /api/order.
func (h *OrderHandler) CreateOrder(w http.ResponseWriter, r *http.Request) {
	var order types.SignedOrder
	if !request.JSON(w, r, &order) {
		return
	}

	result, err := h.orderService.CreateOrder(r.Context(), order)
	if err != nil {
		// Client-input rejection (bad sig / insufficient balance / tick-lot / …):
		// 400 with a stable machine reason code so P5 can branch on it.
		var rejected *service.OrderRejectedError
		if errors.As(err, &rejected) {
			response.JSON(w, http.StatusBadRequest, map[string]any{
				"error":  rejected.Detail,
				"reason": rejected.Reason,
			})
			return
		}
		if errors.Is(err, service.ErrOrderFieldsRequired) {
			response.Error(w, http.StatusBadRequest, err.Error())
			return
		}

		response.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	response.JSON(w, http.StatusOK, result)
}

// CancelOrder handles DELETE /api/order/{id}?owner=<owner>.
//
// {id} is the order's orderHash ("0x…"; the short "ord-…" display id is also
// accepted). owner comes from the query string (MVP trust model — the real
// version should authenticate a signed cancel). Maps: 404 unknown id, 403 not
// the owner, 400 missing owner, 200 with the cancelled order + released state.
func (h *OrderHandler) CancelOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		response.Error(w, http.StatusBadRequest, "order id is required")
		return
	}
	owner := r.URL.Query().Get("owner")

	result, err := h.orderService.CancelOrder(r.Context(), id, owner)
	if err != nil {
		var rejected *service.OrderRejectedError
		if errors.As(err, &rejected) {
			response.JSON(w, http.StatusBadRequest, map[string]any{
				"error":  rejected.Detail,
				"reason": rejected.Reason,
			})
			return
		}
		if errors.Is(err, service.ErrOrderNotFound) {
			response.Error(w, http.StatusNotFound, "order not found")
			return
		}
		if errors.Is(err, service.ErrOrderForbidden) {
			response.Error(w, http.StatusForbidden, "order belongs to another owner")
			return
		}

		response.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	response.JSON(w, http.StatusOK, result)
}

// GetOrderbook handles GET /api/orderbook/{market}.
func (h *OrderHandler) GetOrderbook(w http.ResponseWriter, r *http.Request) {
	market := r.PathValue("market")
	if market == "" {
		response.Error(w, http.StatusBadRequest, "market is required")
		return
	}

	result, err := h.orderService.GetOrderbook(r.Context(), market)
	if err != nil {
		if errors.Is(err, service.ErrMarketNotFound) {
			response.Error(w, http.StatusNotFound, "market not found")
			return
		}

		response.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	response.JSON(w, http.StatusOK, result)
}
