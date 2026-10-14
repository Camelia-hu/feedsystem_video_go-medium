package pay

import (
	"testing"
	"time"
)

func TestPayPostsBalancedBooksAndSettles(t *testing.T) {
	db := testDB(t)
	svc := NewService(db)
	now := time.Unix(1_700_000_000, 0)
	svc.now = func() time.Time { return now }
	ctx := t.Context()
	if err := svc.EnsurePackages(ctx); err != nil {
		t.Fatal(err)
	}
	order, err := svc.CreateRecharge(ctx, 31, CreateRechargeRequest{
		PackageCode: "coins_70", Channel: ChannelMock, IdempotencyKey: "book-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.MockConfirm(ctx, 31, order.OrderNo); err != nil {
		t.Fatal(err)
	}
	book, err := svc.Books(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fee, net := splitFee(700)
	if book.PendingFen != net || book.FeeFen != fee {
		t.Fatalf("book pending=%d fee=%d", book.PendingFen, book.FeeFen)
	}
	var journals []FiatJournal
	if err := db.Where("biz_no = ? AND biz_type = ?", order.OrderNo, BizRecharge).Find(&journals).Error; err != nil {
		t.Fatal(err)
	}
	var debit, credit int64
	for _, row := range journals {
		if row.Direction == DirectionDebit {
			debit += row.AmountFen
		} else {
			credit += row.AmountFen
		}
	}
	if debit != 700 || credit != 700 {
		t.Fatalf("unbalanced debit=%d credit=%d", debit, credit)
	}
	svc.now = func() time.Time { return now.Add(2 * time.Minute) }
	n, err := svc.SettleDue(ctx)
	if err != nil || n != 1 {
		t.Fatalf("settle n=%d err=%v", n, err)
	}
	book, _ = svc.Books(ctx)
	if book.PendingFen != 0 || book.SettledFen != net {
		t.Fatalf("after settle pending=%d settled=%d", book.PendingFen, book.SettledFen)
	}
}

func TestRefundRestoresCoins(t *testing.T) {
	db := testDB(t)
	svc := NewService(db)
	svc.now = func() time.Time { return time.Unix(1_700_000_100, 0) }
	ctx := t.Context()
	if err := svc.EnsurePackages(ctx); err != nil {
		t.Fatal(err)
	}
	order, err := svc.CreateRecharge(ctx, 32, CreateRechargeRequest{
		PackageCode: "coins_70", Channel: ChannelMock, IdempotencyKey: "refund-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.MockConfirm(ctx, 32, order.OrderNo); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Refund(ctx, 32, order.OrderNo)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusRefunded {
		t.Fatalf("status %s", got.Status)
	}
	wallet, err := svc.Wallet(ctx, 32)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.CoinAvailable != 0 {
		t.Fatalf("coins %d", wallet.CoinAvailable)
	}
}
