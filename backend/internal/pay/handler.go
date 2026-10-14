package pay

import (
	"context"
	"errors"
	"io"
	"net/http"

	jwtmw "feedsystem_video_go/internal/middleware/jwt"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) ListPackages(c *gin.Context) {
	items, err := h.svc.ListPackages(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"packages": items})
}

func (h *Handler) CreateRecharge(c *gin.Context) {
	accountID, err := jwtmw.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	var req CreateRechargeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	order, err := h.svc.CreateRecharge(c.Request.Context(), accountID, req)
	if err != nil {
		writePayError(c, err)
		return
	}
	payURL, err := h.svc.CheckoutURL(order)
	if err != nil {
		writePayError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"order": order, "pay_url": payURL})
}

func (h *Handler) QueryRecharge(c *gin.Context) {
	accountID, err := jwtmw.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	var req QueryRechargeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	order, err := h.svc.QueryRecharge(c.Request.Context(), accountID, req.OrderNo)
	if err != nil && !errors.Is(err, ErrOrderExpired) {
		writePayError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"order": order})
}

func (h *Handler) Wallet(c *gin.Context) {
	accountID, err := jwtmw.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	wallet, err := h.svc.Wallet(c.Request.Context(), accountID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"wallet": wallet})
}

func (h *Handler) Ledger(c *gin.Context) {
	accountID, err := jwtmw.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	var req LedgerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := h.svc.Ledger(c.Request.Context(), accountID, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) MockConfirm(c *gin.Context) {
	accountID, err := jwtmw.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	var req MockConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	order, wallet, err := h.svc.MockConfirm(c.Request.Context(), accountID, req.OrderNo)
	if err != nil {
		writePayError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"order": order, "wallet": wallet})
}

func (h *Handler) Books(c *gin.Context) {
	book, err := h.svc.Books(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"book": book, "settle_after_seconds": int(settleAfter().Seconds())})
}

func (h *Handler) ListOrders(c *gin.Context) {
	accountID, err := jwtmw.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	orders, err := h.svc.ListOrders(c.Request.Context(), accountID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"orders": orders})
}

func (h *Handler) Refund(c *gin.Context) {
	h.orderAction(c, h.svc.Refund)
}

func (h *Handler) Chargeback(c *gin.Context) {
	h.orderAction(c, h.svc.Chargeback)
}

func (h *Handler) orderAction(c *gin.Context, fn func(ctx context.Context, accountID uint, orderNo string) (*PayOrder, error)) {
	accountID, err := jwtmw.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	var req OrderActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	order, err := fn(c.Request.Context(), accountID, req.OrderNo)
	if err != nil {
		writePayError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"order": order})
}

func (h *Handler) ListGifts(c *gin.Context) {
	items, err := h.svc.ListGifts(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"gifts": items})
}

func (h *Handler) GiftStat(c *gin.Context) {
	var req struct {
		VideoID uint `json:"video_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	stat, err := h.svc.GiftStat(c.Request.Context(), req.VideoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"stat": stat})
}

func (h *Handler) SendGift(c *gin.Context) {
	accountID, err := jwtmw.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	var req SendGiftRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	order, wallet, err := h.svc.SendGift(c.Request.Context(), accountID, req)
	if err != nil {
		writePayError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"gift": order, "wallet": wallet})
}

func (h *Handler) Withdraw(c *gin.Context) {
	accountID, err := jwtmw.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	var req WithdrawRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	order, err := h.svc.RequestWithdraw(c.Request.Context(), accountID, req.Diamonds)
	if err != nil {
		writePayError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"withdraw": order})
}

func (h *Handler) ListWithdraws(c *gin.Context) {
	accountID, err := jwtmw.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	rows, err := h.svc.ListWithdraws(c.Request.Context(), accountID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"withdraws": rows})
}

func (h *Handler) Callback(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body failed"})
		return
	}
	if err := h.svc.HandleCallback(c.Request.Context(), c.Param("channel"), body, c.GetHeader("X-Pay-Signature")); err != nil {
		writePayError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func writePayError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrPackageNotFound), errors.Is(err, ErrIdempotencyKey), errors.Is(err, ErrChannel), errors.Is(err, ErrOrderExpired), errors.Is(err, ErrOrderClosed), errors.Is(err, ErrAmountMismatch), errors.Is(err, ErrNotPaidNotice), errors.Is(err, ErrRefundNotAllowed), errors.Is(err, ErrInsufficientCoin), errors.Is(err, ErrSelfGift):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, ErrOrderNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, ErrBadSignature):
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
	case errors.Is(err, ErrIdempotencyClash), errors.Is(err, ErrTradeConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
