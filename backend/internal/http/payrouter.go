package http

import (
	"context"
	"log"
	"time"

	"feedsystem_video_go/internal/account"
	"feedsystem_video_go/internal/middleware/jwt"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/pay"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// MountPay 挂载充值支付。feed-api 不调用，避免读服务写入钱包。
func MountPay(r *gin.Engine, db *gorm.DB, accountRepo *account.AccountRepository, cache *rediscache.Client, pop pay.PopularityPublisher) {
	svc := pay.NewService(db)
	svc.SetPopularity(pop)
	if err := svc.EnsurePackages(context.Background()); err != nil {
		log.Printf("pay packages seed failed: %v", err)
	}
	if err := svc.EnsureGifts(context.Background()); err != nil {
		log.Printf("gift catalog seed failed: %v", err)
	}
	h := pay.NewHandler(svc)

	group := r.Group("/pay")
	group.POST("/packages", h.ListPackages)
	group.POST("/callback/:channel", h.Callback)
	group.POST("/gift/catalog", h.ListGifts)
	group.POST("/gift/stat", h.GiftStat)

	protected := group.Group("")
	protected.Use(jwt.JWTAuth(accountRepo, cache))
	{
		protected.POST("/recharge/create", h.CreateRecharge)
		protected.POST("/recharge/query", h.QueryRecharge)
		protected.POST("/wallet", h.Wallet)
		protected.POST("/ledger", h.Ledger)
		protected.POST("/books", h.Books)
		protected.POST("/orders", h.ListOrders)
		protected.POST("/recharge/refund", h.Refund)
		protected.POST("/recharge/chargeback", h.Chargeback)
		protected.POST("/gift/send", h.SendGift)
		protected.POST("/withdraw", h.Withdraw)
		protected.POST("/withdraws", h.ListWithdraws)
	}
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := svc.SettleDue(context.Background()); err != nil {
				log.Printf("pay settle: %v", err)
			}
			if _, err := svc.PayDueWithdraws(context.Background()); err != nil {
				log.Printf("pay withdraw: %v", err)
			}
		}
	}()
}
