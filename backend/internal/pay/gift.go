package pay

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	creatorShareBps = 5000
	diamondFenRate  = 10
	minWithdraw     = 10
	maxGiftCount    = 99
	BizGiftSend     = "gift_send"
	BizGiftReceive  = "gift_receive"
	BizWithdraw     = "withdraw"
)

var ErrSelfGift = errors.New("cannot gift your own video")

type PopularityPublisher interface {
	Update(ctx context.Context, videoID uint, change int64) error
}

type GiftCatalog struct {
	ID               uint   `gorm:"primaryKey" json:"id"`
	Code             string `gorm:"type:varchar(32);uniqueIndex;not null" json:"code"`
	Name             string `gorm:"type:varchar(32);not null" json:"name"`
	CoinPrice        int64  `gorm:"not null" json:"coin_price"`
	PopularityWeight int64  `gorm:"not null" json:"popularity_weight"`
	Status           string `gorm:"type:varchar(16);not null;default:active" json:"status"`
	Sort             int    `gorm:"not null;default:0" json:"sort"`
}

type GiftOrder struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	GiftNo         string    `gorm:"type:varchar(64);uniqueIndex;not null" json:"gift_no"`
	SenderID       uint      `gorm:"not null;uniqueIndex:uk_gift_idem" json:"sender_id"`
	IdempotencyKey string    `gorm:"type:varchar(64);not null;uniqueIndex:uk_gift_idem" json:"idempotency_key"`
	ReceiverID     uint      `gorm:"not null;index" json:"receiver_id"`
	VideoID        uint      `gorm:"not null;index" json:"video_id"`
	GiftCode       string    `gorm:"type:varchar(32);not null" json:"gift_code"`
	Count          int       `gorm:"not null" json:"count"`
	CoinCost       int64     `gorm:"not null" json:"coin_cost"`
	DiamondCredit  int64     `gorm:"not null" json:"diamond_credit"`
	CreatedAt      time.Time `gorm:"autoCreateTime" json:"created_at"`
}

type VideoGiftStat struct {
	VideoID   uint  `gorm:"primaryKey" json:"video_id"`
	GiftCount int64 `gorm:"not null;default:0" json:"gift_count"`
	GiftCoins int64 `gorm:"not null;default:0" json:"gift_coins"`
}

type WithdrawOrder struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	WithdrawNo string     `gorm:"type:varchar(64);uniqueIndex;not null" json:"withdraw_no"`
	AccountID  uint       `gorm:"not null;index" json:"account_id"`
	Diamonds   int64      `gorm:"not null" json:"diamonds"`
	AmountFen  int64      `gorm:"not null" json:"amount_fen"`
	Status     string     `gorm:"type:varchar(16);not null;index" json:"status"`
	CreatedAt  time.Time  `gorm:"autoCreateTime" json:"created_at"`
	PaidAt     *time.Time `json:"paid_at,omitempty"`
}

type SendGiftRequest struct {
	VideoID        uint   `json:"video_id"`
	GiftCode       string `json:"gift_code"`
	Count          int    `json:"count"`
	IdempotencyKey string `json:"idempotency_key"`
}

type WithdrawRequest struct {
	Diamonds int64 `json:"diamonds"`
}

func (s *Service) EnsureGifts(ctx context.Context) error {
	defaults := []GiftCatalog{
		{Code: "rose", Name: "玫瑰", CoinPrice: 2, PopularityWeight: 2, Status: "active", Sort: 1},
		{Code: "bear", Name: "小熊", CoinPrice: 10, PopularityWeight: 10, Status: "active", Sort: 2},
		{Code: "car", Name: "跑车", CoinPrice: 100, PopularityWeight: 80, Status: "active", Sort: 3},
		{Code: "castle", Name: "城堡", CoinPrice: 500, PopularityWeight: 300, Status: "active", Sort: 4},
	}
	for _, item := range defaults {
		var existing GiftCatalog
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

func (s *Service) ListGifts(ctx context.Context) ([]GiftCatalog, error) {
	var items []GiftCatalog
	err := s.db.WithContext(ctx).Where("status = ?", "active").Order("sort ASC").Find(&items).Error
	return items, err
}

func (s *Service) GiftStat(ctx context.Context, videoID uint) (*VideoGiftStat, error) {
	var stat VideoGiftStat
	err := s.db.WithContext(ctx).Where("video_id = ?", videoID).First(&stat).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &VideoGiftStat{VideoID: videoID}, nil
	}
	return &stat, err
}

func (s *Service) SendGift(ctx context.Context, senderID uint, req SendGiftRequest) (*GiftOrder, *Wallet, error) {
	if req.Count <= 0 {
		req.Count = 1
	}
	if req.Count > maxGiftCount || req.VideoID == 0 || req.GiftCode == "" || req.IdempotencyKey == "" {
		return nil, nil, ErrIdempotencyKey
	}
	var existing GiftOrder
	err := s.db.WithContext(ctx).Where("sender_id = ? AND idempotency_key = ?", senderID, req.IdempotencyKey).First(&existing).Error
	if err == nil {
		wallet, werr := s.Wallet(ctx, senderID)
		return &existing, wallet, werr
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, err
	}
	var gift GiftCatalog
	if err := s.db.WithContext(ctx).Where("code = ? AND status = ?", req.GiftCode, "active").First(&gift).Error; err != nil {
		return nil, nil, ErrPackageNotFound
	}
	var video struct {
		ID       uint
		AuthorID uint
	}
	if err := s.db.WithContext(ctx).Table("videos").Select("id, author_id").Where("id = ?", req.VideoID).Take(&video).Error; err != nil {
		return nil, nil, ErrOrderNotFound
	}
	if video.AuthorID == senderID {
		return nil, nil, ErrSelfGift
	}
	cost := gift.CoinPrice * int64(req.Count)
	diamonds := cost * creatorShareBps / 10000
	weight := gift.PopularityWeight * int64(req.Count)
	giftNo, err := newOrderNo(s.now())
	if err != nil {
		return nil, nil, err
	}
	giftNo = "G" + giftNo
	var created GiftOrder
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		sender, receiver, err := lockPair(tx, senderID, video.AuthorID)
		if err != nil {
			return err
		}
		if sender.CoinAvailable < cost {
			return ErrInsufficientCoin
		}
		if err := tx.Model(&Wallet{}).Where("account_id = ?", senderID).Updates(map[string]any{
			"coin_available": sender.CoinAvailable - cost,
			"version":        gorm.Expr("version + 1"),
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&Wallet{}).Where("account_id = ?", video.AuthorID).Updates(map[string]any{
			"diamond_available": receiver.DiamondAvailable + diamonds,
			"version":           gorm.Expr("version + 1"),
		}).Error; err != nil {
			return err
		}
		if err := tx.Create(&LedgerEntry{
			AccountID: senderID, Currency: CurrencyCoin, Direction: DirectionDebit, Amount: cost,
			BalanceAfter: sender.CoinAvailable - cost, BizType: BizGiftSend, BizNo: giftNo, IdempotencyKey: req.IdempotencyKey,
		}).Error; err != nil {
			return err
		}
		if diamonds > 0 {
			if err := tx.Create(&LedgerEntry{
				AccountID: video.AuthorID, Currency: "diamond", Direction: DirectionCredit, Amount: diamonds,
				BalanceAfter: receiver.DiamondAvailable + diamonds, BizType: BizGiftReceive, BizNo: giftNo, IdempotencyKey: req.IdempotencyKey,
			}).Error; err != nil {
				return err
			}
		}
		created = GiftOrder{
			GiftNo: giftNo, SenderID: senderID, IdempotencyKey: req.IdempotencyKey, ReceiverID: video.AuthorID,
			VideoID: req.VideoID, GiftCode: gift.Code, Count: req.Count, CoinCost: cost, DiamondCredit: diamonds,
		}
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "video_id"}}, DoUpdates: clause.Assignments(map[string]any{
			"gift_count": gorm.Expr("gift_count + ?", req.Count),
			"gift_coins": gorm.Expr("gift_coins + ?", cost),
		})}).Create(&VideoGiftStat{VideoID: req.VideoID, GiftCount: int64(req.Count), GiftCoins: cost}).Error; err != nil {
			return err
		}
		return tx.Exec("UPDATE videos SET popularity = popularity + ? WHERE id = ?", weight, req.VideoID).Error
	})
	if err != nil {
		return nil, nil, err
	}
	if s.pop != nil && weight > 0 {
		_ = s.pop.Update(ctx, req.VideoID, weight)
	}
	wallet, err := s.Wallet(ctx, senderID)
	return &created, wallet, err
}

func lockPair(tx *gorm.DB, a, b uint) (*Wallet, *Wallet, error) {
	first, second := a, b
	if second < first {
		first, second = second, first
	}
	w1, err := lockWallet(tx, first)
	if err != nil {
		return nil, nil, err
	}
	w2, err := lockWallet(tx, second)
	if err != nil {
		return nil, nil, err
	}
	if a == first {
		return w1, w2, nil
	}
	return w2, w1, nil
}

func (s *Service) RequestWithdraw(ctx context.Context, accountID uint, diamonds int64) (*WithdrawOrder, error) {
	if diamonds < minWithdraw {
		return nil, ErrRefundNotAllowed
	}
	no, err := newOrderNo(s.now())
	if err != nil {
		return nil, err
	}
	var order WithdrawOrder
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		wallet, err := lockWallet(tx, accountID)
		if err != nil {
			return err
		}
		if wallet.DiamondAvailable < diamonds {
			return ErrInsufficientCoin
		}
		if err := tx.Model(&Wallet{}).Where("account_id = ?", accountID).Updates(map[string]any{
			"diamond_available": wallet.DiamondAvailable - diamonds,
			"diamond_frozen":    wallet.DiamondFrozen + diamonds,
			"version":           gorm.Expr("version + 1"),
		}).Error; err != nil {
			return err
		}
		order = WithdrawOrder{
			WithdrawNo: "W" + no, AccountID: accountID, Diamonds: diamonds,
			AmountFen: diamonds * diamondFenRate, Status: "reviewing", CreatedAt: s.now(),
		}
		return tx.Create(&order).Error
	})
	return &order, err
}

func (s *Service) ListWithdraws(ctx context.Context, accountID uint) ([]WithdrawOrder, error) {
	var rows []WithdrawOrder
	err := s.db.WithContext(ctx).Where("account_id = ?", accountID).Order("id DESC").Limit(20).Find(&rows).Error
	return rows, err
}

func (s *Service) PayDueWithdraws(ctx context.Context) (int, error) {
	deadline := s.now().Add(-15 * time.Second)
	var rows []WithdrawOrder
	err := s.db.WithContext(ctx).Where("status = ? AND created_at <= ?", "reviewing", deadline).Find(&rows).Error
	if err != nil {
		return 0, err
	}
	n := 0
	for _, row := range rows {
		if err := s.payWithdraw(ctx, row.WithdrawNo); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (s *Service) payWithdraw(ctx context.Context, withdrawNo string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order WithdrawOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("withdraw_no = ?", withdrawNo).First(&order).Error; err != nil {
			return err
		}
		if order.Status != "reviewing" {
			return nil
		}
		wallet, err := lockWallet(tx, order.AccountID)
		if err != nil {
			return err
		}
		if wallet.DiamondFrozen < order.Diamonds {
			return tx.Model(&WithdrawOrder{}).Where("id = ?", order.ID).Update("status", StatusFailed).Error
		}
		now := s.now()
		if err := tx.Model(&Wallet{}).Where("account_id = ?", order.AccountID).Updates(map[string]any{
			"diamond_frozen": wallet.DiamondFrozen - order.Diamonds,
			"version":        gorm.Expr("version + 1"),
		}).Error; err != nil {
			return err
		}
		if err := tx.Create(&LedgerEntry{
			AccountID: order.AccountID, Currency: "diamond", Direction: DirectionDebit, Amount: order.Diamonds,
			BalanceAfter: wallet.DiamondAvailable, BizType: BizWithdraw, BizNo: order.WithdrawNo, IdempotencyKey: order.WithdrawNo,
		}).Error; err != nil {
			return err
		}
		book, err := lockBook(tx)
		if err != nil {
			return err
		}
		book.CreatorPayoutFen += order.AmountFen
		if err := saveBook(tx, book); err != nil {
			return err
		}
		return tx.Model(&WithdrawOrder{}).Where("id = ?", order.ID).Updates(map[string]any{"status": StatusPaid, "paid_at": now}).Error
	})
}
