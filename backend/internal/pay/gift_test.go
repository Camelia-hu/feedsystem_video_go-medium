package pay

import (
	"testing"
	"time"

	"feedsystem_video_go/internal/video"
)

func TestGiftAndWithdraw(t *testing.T) {
	db := testDB(t)
	if err := db.AutoMigrate(&video.Video{}); err != nil {
		t.Fatal(err)
	}
	_ = db.Where("id = ?", 91).Delete(&video.Video{}).Error
	if err := db.Create(&video.Video{ID: 91, AuthorID: 2, Username: "author", Title: "t", PlayURL: "p", CoverURL: "c"}).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewService(db)
	now := time.Unix(1_700_000_200, 0)
	svc.now = func() time.Time { return now }
	ctx := t.Context()
	if err := svc.EnsureGifts(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Wallet{AccountID: 3, CoinAvailable: 20}).Error; err != nil {
		t.Fatal(err)
	}
	order, wallet, err := svc.SendGift(ctx, 3, SendGiftRequest{VideoID: 91, GiftCode: "bear", Count: 1, IdempotencyKey: "g1"})
	if err != nil {
		t.Fatal(err)
	}
	if wallet.CoinAvailable != 10 || order.DiamondCredit != 5 {
		t.Fatalf("coins=%d diamonds=%d", wallet.CoinAvailable, order.DiamondCredit)
	}
	if _, _, err := svc.SendGift(ctx, 3, SendGiftRequest{VideoID: 91, GiftCode: "bear", Count: 1, IdempotencyKey: "g1"}); err != nil {
		t.Fatal(err)
	}
	wallet, _ = svc.Wallet(ctx, 3)
	if wallet.CoinAvailable != 10 {
		t.Fatalf("idempotent coins %d", wallet.CoinAvailable)
	}
	if _, _, err := svc.SendGift(ctx, 2, SendGiftRequest{VideoID: 91, GiftCode: "rose", Count: 1, IdempotencyKey: "self"}); err == nil {
		t.Fatal("self gift should fail")
	}
	author, err := svc.Wallet(ctx, 2)
	if err != nil || author.DiamondAvailable != 5 {
		t.Fatalf("author diamonds %+v err=%v", author, err)
	}
	wd, err := svc.RequestWithdraw(ctx, 2, 10)
	if err == nil {
		t.Fatal("should reject below available")
	}
	wd, err = svc.RequestWithdraw(ctx, 2, 5)
	if err == nil {
		t.Fatal("below minimum should fail")
	}
	_ = wd
	if err := db.Model(&Wallet{}).Where("account_id = ?", 2).Update("diamond_available", 20).Error; err != nil {
		t.Fatal(err)
	}
	wd, err = svc.RequestWithdraw(ctx, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if wd.Status != "reviewing" || wd.AmountFen != 100 {
		t.Fatalf("withdraw %+v", wd)
	}
	svc.now = func() time.Time { return now.Add(20 * time.Second) }
	n, err := svc.PayDueWithdraws(ctx)
	if err != nil || n != 1 {
		t.Fatalf("payout n=%d err=%v", n, err)
	}
	author, _ = svc.Wallet(ctx, 2)
	if author.DiamondAvailable != 10 || author.DiamondFrozen != 0 {
		t.Fatalf("after payout %+v", author)
	}
}
