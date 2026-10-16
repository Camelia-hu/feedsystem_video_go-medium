package pay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	acctReceivable = "channel_receivable"
	acctFee        = "channel_fee"
	acctPrepay     = "user_prepay"
	acctBank       = "merchant_bank"
)

var (
	ErrRefundNotAllowed = errors.New("order cannot be refunded")
	ErrInsufficientCoin = errors.New("coins already spent")
)

func splitFee(amount int64) (fee, net int64) {
	if amount <= 0 {
		return 0, 0
	}
	fee = amount * feeBps / 10000
	if fee == 0 {
		fee = 1
	}
	if fee >= amount {
		fee = 1
	}
	return fee, amount - fee
}

func settleAfter() time.Duration {
	v := os.Getenv("PAY_SETTLE_AFTER")
	if v == "" {
		return time.Minute
	}
	sec, err := strconv.Atoi(v)
	if err != nil || sec < 0 {
		return time.Minute
	}
	return time.Duration(sec) * time.Second
}

func postFiat(tx *gorm.DB, bizType, bizNo, account, direction string, amount int64) error {
	if amount == 0 {
		return nil
	}
	row := FiatJournal{Account: account, Direction: direction, AmountFen: amount, BizType: bizType, BizNo: bizNo}
	if err := tx.Create(&row).Error; err != nil && !isDupKey(err) {
		return err
	}
	return nil
}

func lockBook(tx *gorm.DB) (*PlatformBook, error) {
	var book PlatformBook
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&book, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		book = PlatformBook{ID: 1}
		if err := tx.Create(&book).Error; err != nil && !isDupKey(err) {
			return nil, err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&book, 1).Error; err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return &book, nil
}

func saveBook(tx *gorm.DB, book *PlatformBook) error {
	return tx.Save(book).Error
}

func postPayFiat(tx *gorm.DB, order *PayOrder) error {
	fee, net := splitFee(order.AmountFen)
	order.FeeFen = fee
	order.NetFen = net
	order.SettleStatus = SettlePending
	if err := tx.Model(&PayOrder{}).Where("id = ?", order.ID).Updates(map[string]any{
		"fee_fen":       fee,
		"net_fen":       net,
		"settle_status": SettlePending,
	}).Error; err != nil {
		return err
	}
	if err := postFiat(tx, BizRecharge, order.OrderNo, acctReceivable, DirectionDebit, net); err != nil {
		return err
	}
	if err := postFiat(tx, BizRecharge, order.OrderNo, acctFee, DirectionDebit, fee); err != nil {
		return err
	}
	if err := postFiat(tx, BizRecharge, order.OrderNo, acctPrepay, DirectionCredit, order.AmountFen); err != nil {
		return err
	}
	book, err := lockBook(tx)
	if err != nil {
		return err
	}
	book.PendingFen += net
	book.FeeFen += fee
	return saveBook(tx, book)
}

func (s *Service) Books(ctx context.Context) (*PlatformBook, error) {
	var book PlatformBook
	err := s.db.WithContext(ctx).First(&book, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &PlatformBook{ID: 1}, nil
	}
	return &book, err
}

func (s *Service) ListOrders(ctx context.Context, accountID uint) ([]PayOrder, error) {
	var orders []PayOrder
	err := s.db.WithContext(ctx).Where("account_id = ?", accountID).Order("id DESC").Limit(20).Find(&orders).Error
	return orders, err
}

func (s *Service) SettleDue(ctx context.Context) (int, error) {
	deadline := s.now().Add(-settleAfter())
	var orders []PayOrder
	err := s.db.WithContext(ctx).Where("status = ? AND settle_status = ? AND paid_at IS NOT NULL AND paid_at <= ?", StatusPaid, SettlePending, deadline).Find(&orders).Error
	if err != nil {
		return 0, err
	}
	n := 0
	for _, order := range orders {
		if err := s.settleOne(ctx, order.OrderNo); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (s *Service) settleOne(ctx context.Context, orderNo string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order PayOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_no = ?", orderNo).First(&order).Error; err != nil {
			return err
		}
		if order.Status != StatusPaid || order.SettleStatus != SettlePending {
			return nil
		}
		now := s.now()
		if err := tx.Model(&PayOrder{}).Where("id = ? AND settle_status = ?", order.ID, SettlePending).Updates(map[string]any{
			"settle_status": SettleSettled,
			"settle_at":     now,
		}).Error; err != nil {
			return err
		}
		if err := postFiat(tx, BizSettle, order.OrderNo, acctBank, DirectionDebit, order.NetFen); err != nil {
			return err
		}
		if err := postFiat(tx, BizSettle, order.OrderNo, acctReceivable, DirectionCredit, order.NetFen); err != nil {
			return err
		}
		book, err := lockBook(tx)
		if err != nil {
			return err
		}
		book.PendingFen -= order.NetFen
		book.SettledFen += order.NetFen
		return saveBook(tx, book)
	})
}

func (s *Service) Refund(ctx context.Context, accountID uint, orderNo string) (*PayOrder, error) {
	if err := s.gatewayMutate(ctx, orderNo, "/trade/refund"); err != nil {
		return nil, err
	}
	return s.reverse(ctx, accountID, orderNo, false)
}

func (s *Service) Chargeback(ctx context.Context, accountID uint, orderNo string) (*PayOrder, error) {
	if err := s.gatewayMutate(ctx, orderNo, "/trade/chargeback"); err != nil {
		return nil, err
	}
	return s.reverse(ctx, accountID, orderNo, true)
}

func (s *Service) gatewayMutate(ctx context.Context, orderNo, path string) error {
	payload := []byte(`{"order_no":"` + orderNo + `"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gatewayInternalURL()+path, bytes.NewReader(payload))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Pay-Signature", signBody(s.secret, []byte(orderNo)))
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode == http.StatusConflict {
		return ErrRefundNotAllowed
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("gateway %s", resp.Status)
	}
	return nil
}

func (s *Service) reverse(ctx context.Context, accountID uint, orderNo string, chargeback bool) (*PayOrder, error) {
	var updated PayOrder
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order PayOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_no = ? AND account_id = ?", orderNo, accountID).First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOrderNotFound
			}
			return err
		}
		if order.Status != StatusPaid || (order.SettleStatus != SettlePending && order.SettleStatus != SettleSettled) {
			return ErrRefundNotAllowed
		}
		wallet, err := lockWallet(tx, accountID)
		if err != nil {
			return err
		}
		claw := order.Coins
		shortfall := int64(0)
		if wallet.CoinAvailable < claw {
			if !chargeback {
				return ErrInsufficientCoin
			}
			shortfall = claw - wallet.CoinAvailable
			claw = wallet.CoinAvailable
		}
		if err := tx.Model(&Wallet{}).Where("account_id = ?", accountID).Updates(map[string]any{
			"coin_available": wallet.CoinAvailable - claw,
			"version":        gorm.Expr("version + 1"),
		}).Error; err != nil {
			return err
		}
		biz := BizRefund
		nextStatus := StatusRefunded
		settleStatus := SettleRefunded
		if chargeback {
			biz = BizChargeback
			nextStatus = "chargeback"
			settleStatus = SettleChargeback
		}
		if claw > 0 {
			if err := tx.Create(&LedgerEntry{
				AccountID:      accountID,
				Currency:       CurrencyCoin,
				Direction:      DirectionDebit,
				Amount:         claw,
				BalanceAfter:   wallet.CoinAvailable - claw,
				BizType:        biz,
				BizNo:          order.OrderNo,
				IdempotencyKey: order.IdempotencyKey,
			}).Error; err != nil {
				return err
			}
		}
		if order.SettleStatus == SettleSettled {
			if err := postFiat(tx, biz, order.OrderNo, acctReceivable, DirectionDebit, order.NetFen); err != nil {
				return err
			}
			if err := postFiat(tx, biz, order.OrderNo, acctBank, DirectionCredit, order.NetFen); err != nil {
				return err
			}
		}
		if err := postFiat(tx, biz, order.OrderNo, acctPrepay, DirectionDebit, order.AmountFen); err != nil {
			return err
		}
		if err := postFiat(tx, biz, order.OrderNo, acctReceivable, DirectionCredit, order.NetFen); err != nil {
			return err
		}
		if err := postFiat(tx, biz, order.OrderNo, acctFee, DirectionCredit, order.FeeFen); err != nil {
			return err
		}
		book, err := lockBook(tx)
		if err != nil {
			return err
		}
		if order.SettleStatus == SettleSettled {
			book.SettledFen -= order.NetFen
		} else {
			book.PendingFen -= order.NetFen
		}
		book.FeeFen -= order.FeeFen
		if chargeback {
			book.ChargebackFen += order.AmountFen
			book.CoinShortfall += shortfall
		} else {
			book.RefundFen += order.AmountFen
		}
		if err := saveBook(tx, book); err != nil {
			return err
		}
		now := s.now()
		if err := tx.Model(&PayOrder{}).Where("id = ?", order.ID).Updates(map[string]any{
			"status":        nextStatus,
			"settle_status": settleStatus,
			"refunded_at":   now,
		}).Error; err != nil {
			return err
		}
		order.Status = nextStatus
		order.SettleStatus = settleStatus
		order.RefundedAt = &now
		updated = order
		return nil
	})
	return &updated, err
}
