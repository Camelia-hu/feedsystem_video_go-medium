package pay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"feedsystem_video_go/internal/middleware/metrics"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const orderTTL = 15 * time.Minute

var (
	ErrPackageNotFound  = errors.New("package not found")
	ErrIdempotencyKey   = errors.New("idempotency_key is required")
	ErrIdempotencyClash = errors.New("idempotency key already used for another package")
	ErrChannel          = errors.New("unsupported channel")
	ErrOrderNotFound    = errors.New("order not found")
	ErrOrderExpired     = errors.New("order expired")
	ErrOrderClosed      = errors.New("order is not payable")
	ErrBadSignature     = errors.New("invalid callback signature")
	ErrAmountMismatch   = errors.New("callback amount mismatch")
	ErrTradeConflict    = errors.New("channel trade no conflicts with another payment")
	ErrNotPaidNotice    = errors.New("callback is not a successful payment")
)

type Service struct {
	db     *gorm.DB
	secret string
	now    func() time.Time
	pop    PopularityPublisher
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db, secret: callbackSecret(), now: time.Now}
}

func (s *Service) SetPopularity(pop PopularityPublisher) {
	s.pop = pop
}

func (s *Service) EnsurePackages(ctx context.Context) error {
	defaults := []CoinPackage{
		{Code: "coins_70", Title: "70 金币", Coins: 70, BonusCoins: 0, PriceFen: 700, Currency: "CNY", Status: "active", Sort: 1},
		{Code: "coins_350", Title: "350 金币", Coins: 350, BonusCoins: 15, PriceFen: 3500, Currency: "CNY", Status: "active", Sort: 2},
		{Code: "coins_700", Title: "700 金币", Coins: 700, BonusCoins: 50, PriceFen: 7000, Currency: "CNY", Status: "active", Sort: 3},
	}
	for _, item := range defaults {
		var existing CoinPackage
		err := s.db.WithContext(ctx).Where("code = ?", item.Code).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := s.db.WithContext(ctx).Create(&item).Error; err != nil && !isDupKey(err) {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ListPackages(ctx context.Context) ([]CoinPackage, error) {
	var items []CoinPackage
	err := s.db.WithContext(ctx).Where("status = ?", "active").Order("sort ASC, id ASC").Find(&items).Error
	return items, err
}

func (s *Service) CreateRecharge(ctx context.Context, accountID uint, req CreateRechargeRequest) (*PayOrder, error) {
	if accountID == 0 {
		return nil, errors.New("account_id is required")
	}
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.IdempotencyKey == "" || len(req.IdempotencyKey) > 64 {
		return nil, ErrIdempotencyKey
	}
	channel := req.Channel
	if channel == "" {
		channel = ChannelMock
	}
	if !knownChannel(channel) {
		return nil, ErrChannel
	}
	pkg, err := s.findPackage(ctx, req)
	if err != nil {
		return nil, err
	}

	var existing PayOrder
	err = s.db.WithContext(ctx).Where("account_id = ? AND idempotency_key = ?", accountID, req.IdempotencyKey).First(&existing).Error
	if err == nil {
		if existing.PackageCode != pkg.Code || existing.AmountFen != pkg.PriceFen {
			return nil, ErrIdempotencyClash
		}
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	now := s.now()
	orderNo, err := newOrderNo(now)
	if err != nil {
		return nil, err
	}
	order := PayOrder{
		OrderNo:        orderNo,
		AccountID:      accountID,
		IdempotencyKey: req.IdempotencyKey,
		PackageCode:    pkg.Code,
		Coins:          pkg.Coins + pkg.BonusCoins,
		AmountFen:      pkg.PriceFen,
		Currency:       pkg.Currency,
		Channel:        channel,
		Status:         StatusPaying,
		ExpireAt:       now.Add(orderTTL),
	}
	if err := s.db.WithContext(ctx).Create(&order).Error; err != nil {
		if isDupKey(err) {
			err = s.db.WithContext(ctx).Where("account_id = ? AND idempotency_key = ?", accountID, req.IdempotencyKey).First(&existing).Error
			if err != nil {
				return nil, err
			}
			if existing.PackageCode != pkg.Code || existing.AmountFen != pkg.PriceFen {
				return nil, ErrIdempotencyClash
			}
			return &existing, nil
		}
		return nil, err
	}
	metrics.PayOrderTotal.WithLabelValues(channel, StatusPaying).Inc()
	return &order, nil
}

// CheckoutURL 签发跳转到独立收银台的地址。金额和回调地址都在签名里。
func (s *Service) CheckoutURL(order *PayOrder) (string, error) {
	if order == nil || order.OrderNo == "" {
		return "", ErrOrderNotFound
	}
	if order.Status == StatusPaid || order.Status == StatusClosed || order.Status == StatusFailed {
		return "", nil
	}
	q := url.Values{}
	q.Set("order_no", order.OrderNo)
	q.Set("amount_fen", fmt.Sprintf("%d", order.AmountFen))
	q.Set("coins", fmt.Sprintf("%d", order.Coins))
	q.Set("channel", order.Channel)
	q.Set("subject", "ShortVideo金币充值")
	q.Set("notify_url", fmt.Sprintf("%s/pay/callback/%s", notifyBase(), order.Channel))
	q.Set("return_url", fmt.Sprintf("%s/pay/result?order_no=%s", returnBase(), url.QueryEscape(order.OrderNo)))
	q.Set("expire_at", fmt.Sprintf("%d", order.ExpireAt.Unix()))
	q.Set("sign", signValues(s.secret, q))
	return gatewayPublicURL() + "/cashier?" + q.Encode(), nil
}

func (s *Service) QueryRecharge(ctx context.Context, accountID uint, orderNo string) (*PayOrder, error) {
	order, err := s.orderOf(ctx, accountID, orderNo)
	if err != nil {
		return nil, err
	}
	s.compensateFromGateway(ctx, order)
	order, err = s.orderOf(ctx, accountID, orderNo)
	if err != nil {
		return nil, err
	}
	closed, err := s.closeIfExpired(ctx, order)
	if errors.Is(err, ErrOrderExpired) {
		return closed, nil
	}
	return closed, err
}

func (s *Service) compensateFromGateway(ctx context.Context, order *PayOrder) {
	if order == nil || (order.Status != StatusPaying && order.Status != StatusCreated) {
		return
	}
	payload, _ := json.Marshal(map[string]string{"order_no": order.OrderNo})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gatewayInternalURL()+"/trade/query", bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Pay-Signature", signBody(s.secret, []byte(order.OrderNo)))
	client := &http.Client{Timeout: 400 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if err != nil || resp.StatusCode != http.StatusOK {
		return
	}
	if !verifyBody(s.secret, resp.Header.Get("X-Pay-Signature"), raw) {
		return
	}
	var trade struct {
		TradeStatus    string `json:"trade_status"`
		AmountFen      int64  `json:"amount_fen"`
		ChannelTradeNo string `json:"channel_trade_no"`
		Channel        string `json:"channel"`
	}
	if json.Unmarshal(raw, &trade) != nil || trade.AmountFen != order.AmountFen || trade.Channel != order.Channel {
		return
	}
	paid := trade.TradeStatus == "TRADE_SUCCESS"
	if trade.TradeStatus != "TRADE_SUCCESS" && trade.TradeStatus != "TRADE_CLOSED" {
		return
	}
	nonce, err := newToken(4)
	if err != nil {
		return
	}
	body, sig, err := buildCallback(s.secret, CallbackBody{
		OrderNo:        order.OrderNo,
		ChannelTradeNo: trade.ChannelTradeNo,
		AmountFen:      trade.AmountFen,
		Paid:           paid,
		Timestamp:      s.now().Unix(),
		Nonce:          nonce,
	})
	if err != nil {
		return
	}
	_ = s.HandleCallback(ctx, order.Channel, body, sig)
}

func (s *Service) Wallet(ctx context.Context, accountID uint) (*Wallet, error) {
	var wallet Wallet
	err := s.db.WithContext(ctx).Where("account_id = ?", accountID).First(&wallet).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &Wallet{AccountID: accountID}, nil
	}
	if err != nil {
		return nil, err
	}
	return &wallet, nil
}

func (s *Service) Ledger(ctx context.Context, accountID uint, req LedgerRequest) (*LedgerResponse, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	q := s.db.WithContext(ctx).Where("account_id = ?", accountID)
	if req.BeforeID > 0 {
		q = q.Where("id < ?", req.BeforeID)
	}
	var entries []LedgerEntry
	if err := q.Order("id DESC").Limit(limit + 1).Find(&entries).Error; err != nil {
		return nil, err
	}
	resp := &LedgerResponse{Entries: entries}
	if len(entries) > limit {
		resp.HasMore = true
		resp.Entries = entries[:limit]
	}
	if n := len(resp.Entries); n > 0 {
		resp.NextBefore = resp.Entries[n-1].ID
	}
	return resp, nil
}

// MockConfirm 模拟用户在收银台确认支付，然后走同一条验签回调入账。
func (s *Service) MockConfirm(ctx context.Context, accountID uint, orderNo string) (*PayOrder, *Wallet, error) {
	order, err := s.orderOf(ctx, accountID, orderNo)
	if err != nil {
		return nil, nil, err
	}
	order, err = s.closeIfExpired(ctx, order)
	if err != nil {
		return nil, nil, err
	}
	if order.Status == StatusPaid {
		wallet, werr := s.Wallet(ctx, accountID)
		return order, wallet, werr
	}
	if order.Status != StatusCreated && order.Status != StatusPaying {
		return nil, nil, ErrOrderClosed
	}
	if err := s.db.WithContext(ctx).Model(&PayOrder{}).Where("id = ? AND status = ?", order.ID, order.Status).Update("status", StatusPaying).Error; err != nil {
		return nil, nil, err
	}

	trade, err := newToken(8)
	if err != nil {
		return nil, nil, err
	}
	nonce, err := newToken(8)
	if err != nil {
		return nil, nil, err
	}
	now := s.now()
	body, sig, err := buildCallback(s.secret, CallbackBody{
		OrderNo:        order.OrderNo,
		ChannelTradeNo: "M" + trade,
		AmountFen:      order.AmountFen,
		Paid:           true,
		Timestamp:      now.Unix(),
		Nonce:          nonce,
	})
	if err != nil {
		return nil, nil, err
	}
	if err := s.HandleCallback(ctx, ChannelMock, body, sig); err != nil {
		return nil, nil, err
	}
	paid, err := s.orderOf(ctx, accountID, order.OrderNo)
	if err != nil {
		return nil, nil, err
	}
	wallet, err := s.Wallet(ctx, accountID)
	if err != nil {
		return nil, nil, err
	}
	return paid, wallet, nil
}

func (s *Service) HandleCallback(ctx context.Context, channel string, body []byte, signature string) error {
	if !knownChannel(channel) {
		metrics.PayCallbackTotal.WithLabelValues(channel, "rejected").Inc()
		return ErrChannel
	}
	if !verifyBody(s.secret, signature, body) {
		metrics.PayCallbackTotal.WithLabelValues(channel, "bad_signature").Inc()
		return ErrBadSignature
	}
	cb, err := parseCallback(body, s.now())
	if err != nil {
		metrics.PayCallbackTotal.WithLabelValues(channel, "bad_body").Inc()
		return err
	}
	if !cb.Paid {
		if err := s.markFailed(ctx, channel, cb); err != nil {
			metrics.PayCallbackTotal.WithLabelValues(channel, "error").Inc()
			return err
		}
		metrics.PayCallbackTotal.WithLabelValues(channel, "failed_notice").Inc()
		return nil
	}
	credited, err := s.credit(ctx, channel, cb)
	if err != nil {
		metrics.PayCallbackTotal.WithLabelValues(channel, "error").Inc()
		return err
	}
	if credited {
		metrics.PayOrderTotal.WithLabelValues(channel, StatusPaid).Inc()
	}
	metrics.PayCallbackTotal.WithLabelValues(channel, "ok").Inc()
	return nil
}

func (s *Service) markFailed(ctx context.Context, channel string, cb CallbackBody) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order PayOrder
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_no = ?", cb.OrderNo).First(&order).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrOrderNotFound
		}
		if err != nil {
			return err
		}
		if order.Channel != channel || order.AmountFen != cb.AmountFen {
			return ErrAmountMismatch
		}
		if order.Status == StatusPaid || order.Status == StatusFailed || order.Status == StatusClosed {
			return nil
		}
		if order.Status != StatusCreated && order.Status != StatusPaying {
			return ErrOrderClosed
		}
		res := tx.Model(&PayOrder{}).Where("id = ? AND status = ?", order.ID, order.Status).Update("status", StatusFailed)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrTradeConflict
		}
		return nil
	})
}

func (s *Service) credit(ctx context.Context, channel string, cb CallbackBody) (bool, error) {
	credited := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order PayOrder
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_no = ?", cb.OrderNo).First(&order).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrOrderNotFound
		}
		if err != nil {
			return err
		}
		if order.Channel != channel {
			return ErrChannel
		}
		if order.Status == StatusPaid {
			if order.ChannelTradeNo != nil && *order.ChannelTradeNo == cb.ChannelTradeNo {
				return nil
			}
			return ErrTradeConflict
		}
		if order.AmountFen != cb.AmountFen {
			return ErrAmountMismatch
		}
		switch order.Status {
		case StatusCreated, StatusPaying, StatusClosed:
		default:
			return ErrOrderClosed
		}

		now := s.now()
		trade := cb.ChannelTradeNo
		res := tx.Model(&PayOrder{}).Where("id = ? AND status = ?", order.ID, order.Status).Updates(map[string]any{
			"status":           StatusPaid,
			"channel_trade_no": trade,
			"paid_at":          now,
			"paid_after_close": order.Status == StatusClosed,
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrTradeConflict
		}

		wallet, err := lockWallet(tx, order.AccountID)
		if err != nil {
			return err
		}
		next := wallet.CoinAvailable + order.Coins
		if err := tx.Model(&Wallet{}).Where("account_id = ?", order.AccountID).Updates(map[string]any{
			"coin_available": next,
			"version":        gorm.Expr("version + 1"),
		}).Error; err != nil {
			return err
		}
		entry := LedgerEntry{
			AccountID:      order.AccountID,
			Currency:       CurrencyCoin,
			Direction:      DirectionCredit,
			Amount:         order.Coins,
			BalanceAfter:   next,
			BizType:        BizRecharge,
			BizNo:          order.OrderNo,
			IdempotencyKey: order.IdempotencyKey,
		}
		if err := tx.Create(&entry).Error; err != nil {
			return err
		}
		if err := postPayFiat(tx, &order); err != nil {
			return err
		}
		logRow := PayCallbackLog{
			Channel:        channel,
			ChannelTradeNo: cb.ChannelTradeNo,
			OrderNo:        cb.OrderNo,
			AmountFen:      cb.AmountFen,
			SignatureOK:    true,
			Processed:      true,
		}
		if err := tx.Create(&logRow).Error; err != nil && !isDupKey(err) {
			return err
		}
		credited = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return credited, nil
}

func lockWallet(tx *gorm.DB, accountID uint) (*Wallet, error) {
	var wallet Wallet
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ?", accountID).First(&wallet).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		wallet = Wallet{AccountID: accountID}
		if err := tx.Create(&wallet).Error; err != nil && !isDupKey(err) {
			return nil, err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ?", accountID).First(&wallet).Error; err != nil {
			return nil, err
		}
		return &wallet, nil
	}
	if err != nil {
		return nil, err
	}
	return &wallet, nil
}

func (s *Service) findPackage(ctx context.Context, req CreateRechargeRequest) (*CoinPackage, error) {
	var pkg CoinPackage
	q := s.db.WithContext(ctx).Where("status = ?", "active")
	if req.PackageID > 0 {
		q = q.Where("id = ?", req.PackageID)
	} else if req.PackageCode != "" {
		q = q.Where("code = ?", req.PackageCode)
	} else {
		return nil, ErrPackageNotFound
	}
	err := q.First(&pkg).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPackageNotFound
	}
	if err != nil {
		return nil, err
	}
	if pkg.PriceFen <= 0 || pkg.Coins < 0 || pkg.BonusCoins < 0 {
		return nil, ErrPackageNotFound
	}
	return &pkg, nil
}

func (s *Service) orderOf(ctx context.Context, accountID uint, orderNo string) (*PayOrder, error) {
	if orderNo == "" {
		return nil, ErrOrderNotFound
	}
	var order PayOrder
	err := s.db.WithContext(ctx).Where("order_no = ? AND account_id = ?", orderNo, accountID).First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrOrderNotFound
	}
	if err != nil {
		return nil, err
	}
	return &order, nil
}

func (s *Service) closeIfExpired(ctx context.Context, order *PayOrder) (*PayOrder, error) {
	if order.Status != StatusCreated && order.Status != StatusPaying {
		return order, nil
	}
	if !s.now().After(order.ExpireAt) {
		return order, nil
	}
	res := s.db.WithContext(ctx).Model(&PayOrder{}).
		Where("id = ? AND status IN ?", order.ID, []string{StatusCreated, StatusPaying}).
		Update("status", StatusClosed)
	if res.Error != nil {
		return nil, res.Error
	}
	order.Status = StatusClosed
	s.releaseGatewayHold(ctx, order.OrderNo)
	return order, ErrOrderExpired
}

func (s *Service) releaseGatewayHold(ctx context.Context, orderNo string) {
	payload := []byte(`{"order_no":"` + orderNo + `"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gatewayInternalURL()+"/trade/close", bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Pay-Signature", signBody(s.secret, []byte(orderNo)))
	client := &http.Client{Timeout: 400 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func isDupKey(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}
