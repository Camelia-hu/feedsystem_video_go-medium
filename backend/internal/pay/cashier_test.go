package pay

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestCashierNotifyCreditsOnce(t *testing.T) {
	db := testDB(t)
	svc := NewService(db)
	svc.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	if err := svc.EnsurePackages(t.Context()); err != nil {
		t.Fatal(err)
	}
	order, err := svc.CreateRecharge(t.Context(), 21, CreateRechargeRequest{
		PackageCode:    "coins_70",
		Channel:        ChannelAlipay,
		IdempotencyKey: "gw-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	api := gin.New()
	api.POST("/pay/callback/:channel", NewHandler(svc).Callback)
	merchant := httptest.NewServer(api)
	defer merchant.Close()

	t.Setenv("PAY_NOTIFY_BASE", merchant.URL)
	t.Setenv("PAY_RETURN_BASE", "http://app.local")
	t.Setenv("PAY_NOTIFY_DELAY_MS", "0")
	payURL, err := svc.CheckoutURL(order)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(payURL)
	if err != nil {
		t.Fatal(err)
	}

	cashier := NewCashier(svc.secret)
	cashier.now = svc.now
	gwRouter := gin.New()
	cashier.Register(gwRouter)
	gw := httptest.NewServer(gwRouter)
	defer gw.Close()

	page, err := http.Get(gw.URL + "/cashier?" + parsed.RawQuery)
	if err != nil {
		t.Fatal(err)
	}
	page.Body.Close()
	if page.StatusCode != http.StatusOK {
		t.Fatalf("cashier status %d", page.StatusCode)
	}

	form := parsed.Query()
	form.Set("sandbox_password", "111111")
	form.Set("action", "success")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.PostForm(gw.URL+"/cashier/submit", form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("submit status %d", res.StatusCode)
	}
	wallet, err := svc.Wallet(t.Context(), 21)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.CoinAvailable != 70 {
		t.Fatalf("balance %d", wallet.CoinAvailable)
	}

	res, err = client.PostForm(gw.URL+"/cashier/submit", form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	wallet, err = svc.Wallet(t.Context(), 21)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.CoinAvailable != 70 {
		t.Fatalf("second notify balance %d", wallet.CoinAvailable)
	}
}

func TestQueryCompensatesBeforeNotify(t *testing.T) {
	db := testDB(t)
	svc := NewService(db)
	svc.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	if err := svc.EnsurePackages(t.Context()); err != nil {
		t.Fatal(err)
	}
	order, err := svc.CreateRecharge(t.Context(), 22, CreateRechargeRequest{
		PackageCode:    "coins_70",
		Channel:        ChannelWechat,
		IdempotencyKey: "gw-compensate",
	})
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	api := gin.New()
	api.POST("/pay/callback/:channel", NewHandler(svc).Callback)
	merchant := httptest.NewServer(api)
	defer merchant.Close()
	t.Setenv("PAY_NOTIFY_BASE", merchant.URL)
	t.Setenv("PAY_RETURN_BASE", "http://app.local")
	t.Setenv("PAY_NOTIFY_DELAY_MS", "5000")
	payURL, err := svc.CheckoutURL(order)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(payURL)
	if err != nil {
		t.Fatal(err)
	}
	cashier := NewCashier(svc.secret)
	cashier.now = svc.now
	gwRouter := gin.New()
	cashier.Register(gwRouter)
	gw := httptest.NewServer(gwRouter)
	defer gw.Close()
	t.Setenv("PAY_GATEWAY_INTERNAL_URL", gw.URL)

	form := parsed.Query()
	form.Set("sandbox_password", "111111")
	form.Set("action", "success")
	res, err := http.PostForm(gw.URL+"/cashier/submit", form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	got, err := svc.QueryRecharge(t.Context(), 22, order.OrderNo)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusPaid {
		t.Fatalf("compensated status %s", got.Status)
	}
}
