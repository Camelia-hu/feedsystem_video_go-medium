package pay

import "time"

const (
	StatusCreated   = "created"
	StatusPaying    = "paying"
	StatusPaid      = "paid"
	StatusClosed    = "closed"
	StatusRefunding = "refunding"
	StatusRefunded  = "refunded"
	StatusFailed    = "failed"

	ChannelMock   = "mock"
	ChannelAlipay = "alipay"
	ChannelWechat = "wechat"
	ChannelCard   = "card"

	CurrencyCoin    = "coin"
	CurrencyFen     = "fen"
	DirectionCredit = "credit"
	DirectionDebit  = "debit"
	BizRecharge     = "recharge"
	BizRefund       = "refund"
	BizSettle       = "settle"
	BizChargeback   = "chargeback"

	SettleNone       = "none"
	SettlePending    = "pending"
	SettleSettled    = "settled"
	SettleRefunded   = "refunded"
	SettleChargeback = "chargeback"

	feeBps = 60
)

type Wallet struct {
	AccountID        uint      `gorm:"primaryKey" json:"account_id"`
	CoinAvailable    int64     `gorm:"not null;default:0" json:"coin_available"`
	CoinFrozen       int64     `gorm:"not null;default:0" json:"coin_frozen"`
	DiamondAvailable int64     `gorm:"not null;default:0" json:"diamond_available"`
	DiamondFrozen    int64     `gorm:"not null;default:0" json:"diamond_frozen"`
	Version          int64     `gorm:"not null;default:0" json:"-"`
	UpdatedAt        time.Time `json:"-"`
}

type LedgerEntry struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	AccountID      uint      `gorm:"not null;index:idx_ledger_account;uniqueIndex:uk_ledger_biz" json:"account_id"`
	Currency       string    `gorm:"type:varchar(16);not null" json:"currency"`
	Direction      string    `gorm:"type:varchar(16);not null;uniqueIndex:uk_ledger_biz" json:"direction"`
	Amount         int64     `gorm:"not null" json:"amount"`
	BalanceAfter   int64     `gorm:"not null" json:"balance_after"`
	BizType        string    `gorm:"type:varchar(32);not null;uniqueIndex:uk_ledger_biz" json:"biz_type"`
	BizNo          string    `gorm:"type:varchar(64);not null;uniqueIndex:uk_ledger_biz" json:"biz_no"`
	IdempotencyKey string    `gorm:"type:varchar(64);not null" json:"idempotency_key"`
	CreatedAt      time.Time `gorm:"autoCreateTime" json:"created_at"`
}

type CoinPackage struct {
	ID         uint   `gorm:"primaryKey" json:"id"`
	Code       string `gorm:"type:varchar(32);uniqueIndex;not null" json:"code"`
	Title      string `gorm:"type:varchar(64);not null" json:"title"`
	Coins      int64  `gorm:"not null" json:"coins"`
	BonusCoins int64  `gorm:"not null;default:0" json:"bonus_coins"`
	PriceFen   int64  `gorm:"not null" json:"price_fen"`
	Currency   string `gorm:"type:varchar(8);not null;default:CNY" json:"currency"`
	Status     string `gorm:"type:varchar(16);not null;default:active;index" json:"status"`
	Sort       int    `gorm:"not null;default:0" json:"sort"`
}

type PayOrder struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	OrderNo        string     `gorm:"type:varchar(64);uniqueIndex;not null" json:"order_no"`
	AccountID      uint       `gorm:"not null;uniqueIndex:uk_pay_idem" json:"account_id"`
	IdempotencyKey string     `gorm:"type:varchar(64);not null;uniqueIndex:uk_pay_idem" json:"idempotency_key"`
	PackageCode    string     `gorm:"type:varchar(32);not null" json:"package_code"`
	Coins          int64      `gorm:"not null" json:"coins"`
	AmountFen      int64      `gorm:"not null" json:"amount_fen"`
	Currency       string     `gorm:"type:varchar(8);not null" json:"currency"`
	Channel        string     `gorm:"type:varchar(32);not null" json:"channel"`
	Status         string     `gorm:"type:varchar(16);not null;index" json:"status"`
	ChannelTradeNo *string    `gorm:"type:varchar(64);uniqueIndex" json:"channel_trade_no,omitempty"`
	PaidAfterClose bool       `gorm:"not null;default:false" json:"paid_after_close"`
	FeeFen         int64      `gorm:"not null;default:0" json:"fee_fen"`
	NetFen         int64      `gorm:"not null;default:0" json:"net_fen"`
	SettleStatus   string     `gorm:"type:varchar(16);not null;default:none;index" json:"settle_status"`
	SettleAt       *time.Time `json:"settle_at,omitempty"`
	ExpireAt       time.Time  `gorm:"not null" json:"expire_at"`
	PaidAt         *time.Time `json:"paid_at,omitempty"`
	RefundedAt     *time.Time `json:"refunded_at,omitempty"`
	CreatedAt      time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt      time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

type PayCallbackLog struct {
	ID             uint      `gorm:"primaryKey"`
	Channel        string    `gorm:"type:varchar(32);not null;uniqueIndex:uk_pay_callback"`
	ChannelTradeNo string    `gorm:"type:varchar(64);not null;uniqueIndex:uk_pay_callback"`
	OrderNo        string    `gorm:"type:varchar(64);not null;index"`
	AmountFen      int64     `gorm:"not null"`
	SignatureOK    bool      `gorm:"not null"`
	Processed      bool      `gorm:"not null"`
	CreatedAt      time.Time `gorm:"autoCreateTime"`
}

type PlatformBook struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	PendingFen       int64     `gorm:"not null;default:0" json:"pending_fen"`
	SettledFen       int64     `gorm:"not null;default:0" json:"settled_fen"`
	FeeFen           int64     `gorm:"not null;default:0" json:"fee_fen"`
	RefundFen        int64     `gorm:"not null;default:0" json:"refund_fen"`
	ChargebackFen    int64     `gorm:"not null;default:0" json:"chargeback_fen"`
	CoinShortfall    int64     `gorm:"not null;default:0" json:"coin_shortfall"`
	CreatorPayoutFen int64     `gorm:"not null;default:0" json:"creator_payout_fen"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type FiatJournal struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Account   string    `gorm:"type:varchar(32);not null;uniqueIndex:uk_fiat" json:"account"`
	Direction string    `gorm:"type:varchar(16);not null;uniqueIndex:uk_fiat" json:"direction"`
	AmountFen int64     `gorm:"not null" json:"amount_fen"`
	BizType   string    `gorm:"type:varchar(32);not null;uniqueIndex:uk_fiat" json:"biz_type"`
	BizNo     string    `gorm:"type:varchar(64);not null;uniqueIndex:uk_fiat" json:"biz_no"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}

type GatewayTrade struct {
	OrderNo        string     `gorm:"primaryKey;type:varchar(64)" json:"order_no"`
	AmountFen      int64      `gorm:"not null" json:"amount_fen"`
	FrozenFen      int64      `gorm:"not null;default:0" json:"frozen_fen"`
	Channel        string     `gorm:"type:varchar(32);not null" json:"channel"`
	Status         string     `gorm:"type:varchar(32);not null;index" json:"status"`
	ChannelTradeNo string     `gorm:"type:varchar(64)" json:"channel_trade_no"`
	NotifyURL      string     `gorm:"type:varchar(255)" json:"-"`
	NotifyAction   string     `gorm:"type:varchar(16)" json:"-"`
	NotifyPaid     bool       `json:"-"`
	NotifyStatus   string     `gorm:"type:varchar(16);not null;default:none;index" json:"notify_status"`
	NotifyAttempts int        `gorm:"not null;default:0" json:"notify_attempts"`
	NextNotifyAt   *time.Time `json:"-"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type CreateRechargeRequest struct {
	PackageCode    string `json:"package_code"`
	PackageID      uint   `json:"package_id"`
	Channel        string `json:"channel"`
	IdempotencyKey string `json:"idempotency_key"`
}

type QueryRechargeRequest struct {
	OrderNo string `json:"order_no"`
}

type MockConfirmRequest struct {
	OrderNo string `json:"order_no"`
}

type OrderActionRequest struct {
	OrderNo string `json:"order_no"`
}

type LedgerRequest struct {
	BeforeID uint `json:"before_id"`
	Limit    int  `json:"limit"`
}

type LedgerResponse struct {
	Entries    []LedgerEntry `json:"entries"`
	NextBefore uint          `json:"next_before,omitempty"`
	HasMore    bool          `json:"has_more"`
}

type CallbackBody struct {
	OrderNo        string `json:"order_no"`
	ChannelTradeNo string `json:"channel_trade_no"`
	AmountFen      int64  `json:"amount_fen"`
	Paid           bool   `json:"paid"`
	Timestamp      int64  `json:"timestamp"`
	Nonce          string `json:"nonce"`
}
