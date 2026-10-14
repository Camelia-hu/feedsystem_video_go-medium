package pay

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	host := os.Getenv("PAY_TEST_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("PAY_TEST_PORT")
	if port == "" {
		port = "3307"
	}
	user := os.Getenv("PAY_TEST_USER")
	if user == "" {
		user = "root"
	}
	pass := os.Getenv("PAY_TEST_PASSWORD")
	if pass == "" {
		pass = "123456"
	}
	rootDSN := fmt.Sprintf("%s:%s@tcp(%s:%s)/?charset=utf8mb4&parseTime=True&loc=Local", user, pass, host, port)
	root, err := gorm.Open(mysql.Open(rootDSN), &gorm.Config{})
	if err != nil {
		t.Skipf("mysql unavailable: %v", err)
	}
	if err := root.Exec("CREATE DATABASE IF NOT EXISTS feedsystem_pay_test").Error; err != nil {
		t.Skipf("create test database: %v", err)
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/feedsystem_pay_test?charset=utf8mb4&parseTime=True&loc=Local", user, pass, host, port)
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Skipf("open test database: %v", err)
	}
	if err := db.AutoMigrate(&Wallet{}, &LedgerEntry{}, &CoinPackage{}, &PayOrder{}, &PayCallbackLog{}, &PlatformBook{}, &FiatJournal{}, &GatewayTrade{}, &GiftCatalog{}, &GiftOrder{}, &VideoGiftStat{}, &WithdrawOrder{}); err != nil {
		t.Fatal(err)
	}
	db.Where("1 = 1").Delete(&FiatJournal{})
	db.Where("1 = 1").Delete(&GatewayTrade{})
	db.Where("1 = 1").Delete(&PayCallbackLog{})
	db.Where("1 = 1").Delete(&LedgerEntry{})
	db.Where("1 = 1").Delete(&PayOrder{})
	db.Where("1 = 1").Delete(&Wallet{})
	db.Where("1 = 1").Delete(&CoinPackage{})
	db.Where("1 = 1").Delete(&PlatformBook{})
	return db
}

func TestRechargeCreditsOnce(t *testing.T) {
	db := testDB(t)
	svc := NewService(db)
	svc.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	ctx := context.Background()
	if err := svc.EnsurePackages(ctx); err != nil {
		t.Fatal(err)
	}

	first, err := svc.CreateRecharge(ctx, 7, CreateRechargeRequest{
		PackageCode:    "coins_70",
		Channel:        ChannelMock,
		IdempotencyKey: "key-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	again, err := svc.CreateRecharge(ctx, 7, CreateRechargeRequest{
		PackageCode:    "coins_70",
		Channel:        ChannelMock,
		IdempotencyKey: "key-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.OrderNo != first.OrderNo {
		t.Fatalf("idempotent create got %s want %s", again.OrderNo, first.OrderNo)
	}

	order, wallet, err := svc.MockConfirm(ctx, 7, first.OrderNo)
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != StatusPaid || wallet.CoinAvailable != 70 {
		t.Fatalf("order=%s coins=%d", order.Status, wallet.CoinAvailable)
	}
	order, wallet, err = svc.MockConfirm(ctx, 7, first.OrderNo)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.CoinAvailable != 70 {
		t.Fatalf("double confirm credited again: %d", wallet.CoinAvailable)
	}

	body, sig, err := buildCallback(svc.secret, CallbackBody{
		OrderNo:        first.OrderNo,
		ChannelTradeNo: "OTHER",
		AmountFen:      first.AmountFen,
		Paid:           true,
		Timestamp:      svc.now().Unix(),
		Nonce:          "n2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleCallback(ctx, ChannelMock, body, sig); !errors.Is(err, ErrTradeConflict) {
		t.Fatalf("conflict err=%v", err)
	}
	wallet, err = svc.Wallet(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.CoinAvailable != 70 {
		t.Fatalf("conflict changed balance: %d", wallet.CoinAvailable)
	}
}

func TestExpiredOrderRejectsMockConfirm(t *testing.T) {
	db := testDB(t)
	svc := NewService(db)
	now := time.Unix(1_700_000_000, 0)
	svc.now = func() time.Time { return now }
	ctx := context.Background()
	if err := svc.EnsurePackages(ctx); err != nil {
		t.Fatal(err)
	}
	order, err := svc.CreateRecharge(ctx, 8, CreateRechargeRequest{
		PackageCode:    "coins_350",
		IdempotencyKey: "key-expire",
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return now.Add(16 * time.Minute) }
	if _, _, err := svc.MockConfirm(ctx, 8, order.OrderNo); !errors.Is(err, ErrOrderExpired) {
		t.Fatalf("err=%v", err)
	}
	wallet, err := svc.Wallet(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.CoinAvailable != 0 {
		t.Fatalf("expired confirm credited %d", wallet.CoinAvailable)
	}
}
