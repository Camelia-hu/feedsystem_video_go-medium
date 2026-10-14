package pay

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	sandboxPayPassword = "111111"
	testCardSuccess    = "4242424242424242"
	testCardDecline    = "4000000000000002"
	tradeWait          = "WAIT_BUYER_PAY"
	tradeSuccess       = "TRADE_SUCCESS"
	tradeClosed        = "TRADE_CLOSED"
	tradeRefund        = "TRADE_REFUND"
	tradeChargeback    = "TRADE_CHARGEBACK"
)

type gatewayTrade struct {
	OrderNo        string
	AmountFen      int64
	Channel        string
	Status         string
	ChannelTradeNo string
	NotifyURL      string
}

type Cashier struct {
	secret string
	client *http.Client
	now    func() time.Time
	mu     sync.Mutex
	trades map[string]gatewayTrade
	db     *gorm.DB
}

func NewCashier(secret string) *Cashier {
	return NewCashierWithDB(secret, nil)
}

func NewCashierWithDB(secret string, db *gorm.DB) *Cashier {
	if secret == "" {
		secret = callbackSecret()
	}
	return &Cashier{
		secret: secret,
		client: &http.Client{Timeout: 3 * time.Second},
		now:    time.Now,
		trades: map[string]gatewayTrade{},
		db:     db,
	}
}

func (c *Cashier) Register(r *gin.Engine) {
	r.GET("/healthz", func(ctx *gin.Context) { ctx.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/cashier", c.Show)
	r.POST("/cashier/submit", c.Submit)
	r.POST("/trade/query", c.QueryTrade)
	r.POST("/trade/close", c.CloseTrade)
	r.POST("/trade/refund", c.RefundTrade)
	r.POST("/trade/chargeback", c.ChargebackTrade)
}

func (c *Cashier) Show(ctx *gin.Context) {
	q := ctx.Request.URL.Query()
	if err := c.validate(q); err != nil {
		ctx.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte(renderMessage("无法打开收银台", err.Error())))
		return
	}
	amount, _ := strconv.ParseInt(q.Get("amount_fen"), 10, 64)
	c.remember(gatewayTrade{
		OrderNo:   q.Get("order_no"),
		AmountFen: amount,
		Channel:   q.Get("channel"),
		Status:    tradeWait,
		NotifyURL: q.Get("notify_url"),
	})
	ctx.Data(http.StatusOK, "text/html; charset=utf-8", []byte(renderCashier(q)))
}

func (c *Cashier) Submit(ctx *gin.Context) {
	if err := ctx.Request.ParseForm(); err != nil {
		ctx.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte(renderMessage("支付失败", "表单无效")))
		return
	}
	q := ctx.Request.PostForm
	if err := c.validate(q); err != nil {
		ctx.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte(renderMessage("支付失败", err.Error())))
		return
	}
	paid, action, err := c.authorize(q)
	if err != nil {
		ctx.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte(renderMessage("支付失败", err.Error())))
		return
	}
	amount, _ := strconv.ParseInt(q.Get("amount_fen"), 10, 64)
	tradeNo := "F" + q.Get("order_no")
	status := tradeClosed
	if paid {
		tradeNo = "T" + q.Get("order_no")
		status = tradeSuccess
	}
	c.remember(gatewayTrade{
		OrderNo:        q.Get("order_no"),
		AmountFen:      amount,
		Channel:        q.Get("channel"),
		Status:         status,
		ChannelTradeNo: tradeNo,
		NotifyURL:      q.Get("notify_url"),
	})
	ret, err := url.Parse(q.Get("return_url"))
	if err != nil {
		ctx.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte(renderMessage("支付完成", "回跳地址无效")))
		return
	}
	rq := ret.Query()
	rq.Set("channel_status", action)
	ret.RawQuery = rq.Encode()
	returnURL := ret.String()
	notifyCopy := cloneValues(q)
	if notifyDelay() == 0 {
		if err := c.notify(notifyCopy, paid, action); err != nil {
			c.enqueueNotify(notifyCopy, paid, action)
		}
		ctx.Data(http.StatusOK, "text/html; charset=utf-8", []byte(renderProcessing(returnURL, 600)))
		return
	}
	go func() {
		time.Sleep(notifyDelay())
		if err := c.notify(notifyCopy, paid, action); err != nil {
			c.enqueueNotify(notifyCopy, paid, action)
		}
	}()
	ctx.Data(http.StatusOK, "text/html; charset=utf-8", []byte(renderProcessing(returnURL, 900)))
}

func (c *Cashier) QueryTrade(ctx *gin.Context) {
	var req struct {
		OrderNo string `json:"order_no"`
	}
	body, err := io.ReadAll(ctx.Request.Body)
	if err != nil || json.Unmarshal(body, &req) != nil || req.OrderNo == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "order_no is required"})
		return
	}
	if !hmacEqual(signBody(c.secret, []byte(req.OrderNo)), ctx.GetHeader("X-Pay-Signature")) {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}
	trade, ok := c.tradeOf(req.OrderNo)
	if !ok {
		ctx.JSON(http.StatusOK, gin.H{"order_no": req.OrderNo, "trade_status": "NOT_EXIST"})
		return
	}
	payload := tradeQueryBody{
		OrderNo:        trade.OrderNo,
		TradeStatus:    trade.Status,
		AmountFen:      trade.AmountFen,
		ChannelTradeNo: trade.ChannelTradeNo,
		Channel:        trade.Channel,
	}
	raw, _ := json.Marshal(payload)
	ctx.Header("X-Pay-Signature", signBody(c.secret, raw))
	ctx.Data(http.StatusOK, "application/json", raw)
}

func signedFields(q url.Values) url.Values {
	out := url.Values{}
	for _, k := range []string{"order_no", "amount_fen", "coins", "channel", "subject", "notify_url", "return_url", "expire_at", "sign"} {
		if q.Get(k) != "" {
			out.Set(k, q.Get(k))
		}
	}
	return out
}

func (c *Cashier) validate(q url.Values) error {
	if !verifyValues(c.secret, signedFields(q)) {
		return fmt.Errorf("收银台签名无效")
	}
	amount, err := strconv.ParseInt(q.Get("amount_fen"), 10, 64)
	if err != nil || amount <= 0 || q.Get("order_no") == "" || q.Get("notify_url") == "" {
		return fmt.Errorf("收银台参数不完整")
	}
	expireAt, err := strconv.ParseInt(q.Get("expire_at"), 10, 64)
	if err != nil {
		return fmt.Errorf("过期时间无效")
	}
	if c.now().Unix() > expireAt {
		return fmt.Errorf("订单已过期")
	}
	if !knownChannel(q.Get("channel")) {
		return fmt.Errorf("不支持的支付渠道")
	}
	return nil
}

func (c *Cashier) authorize(q url.Values) (bool, string, error) {
	action := q.Get("action")
	if action == "cancel" || action == "fail" {
		return false, action, nil
	}
	if action != "success" {
		return false, "", fmt.Errorf("未知操作")
	}
	if q.Get("channel") == ChannelCard {
		return authorizeCard(q)
	}
	if q.Get("sandbox_password") != sandboxPayPassword {
		return false, "", fmt.Errorf("支付密码错误，沙箱密码是 %s", sandboxPayPassword)
	}
	return true, "success", nil
}

func authorizeCard(q url.Values) (bool, string, error) {
	pan := digitsOnly(q.Get("card_number"))
	exp := strings.TrimSpace(q.Get("card_exp"))
	cvv := digitsOnly(q.Get("card_cvv"))
	if len(exp) < 4 || len(cvv) < 3 {
		return false, "", fmt.Errorf("请填写有效期和 CVV")
	}
	switch pan {
	case testCardSuccess:
		return true, "success", nil
	case testCardDecline:
		return false, "fail", nil
	default:
		return false, "", fmt.Errorf("沙箱只接受测试卡 %s 或 %s", testCardSuccess, testCardDecline)
	}
}

func (c *Cashier) remember(trade gatewayTrade) {
	c.mu.Lock()
	if prev, ok := c.trades[trade.OrderNo]; ok && prev.Status == tradeSuccess && trade.Status != tradeSuccess && trade.Status != tradeRefund && trade.Status != tradeChargeback {
		c.mu.Unlock()
		return
	}
	c.trades[trade.OrderNo] = trade
	c.mu.Unlock()
	if c.db == nil {
		return
	}
	row := GatewayTrade{
		OrderNo: trade.OrderNo, AmountFen: trade.AmountFen, FrozenFen: frozenOf(trade),
		Channel: trade.Channel, Status: trade.Status, ChannelTradeNo: trade.ChannelTradeNo,
		NotifyURL: trade.NotifyURL, UpdatedAt: c.now(),
	}
	_ = c.db.Save(&row).Error
}

func frozenOf(trade gatewayTrade) int64 {
	if trade.Status == tradeWait {
		return trade.AmountFen
	}
	return 0
}

func (c *Cashier) tradeOf(orderNo string) (gatewayTrade, bool) {
	if c.db != nil {
		var row GatewayTrade
		if err := c.db.Where("order_no = ?", orderNo).First(&row).Error; err == nil {
			return gatewayTrade{OrderNo: row.OrderNo, AmountFen: row.AmountFen, Channel: row.Channel, Status: row.Status, ChannelTradeNo: row.ChannelTradeNo, NotifyURL: row.NotifyURL}, true
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	trade, ok := c.trades[orderNo]
	return trade, ok
}

func (c *Cashier) CloseTrade(ctx *gin.Context) {
	c.mutateTrade(ctx, tradeClosed)
}

func (c *Cashier) RefundTrade(ctx *gin.Context) {
	c.mutateTrade(ctx, tradeRefund)
}

func (c *Cashier) ChargebackTrade(ctx *gin.Context) {
	c.mutateTrade(ctx, tradeChargeback)
}

func (c *Cashier) mutateTrade(ctx *gin.Context, next string) {
	orderNo, ok := c.signedOrderNo(ctx)
	if !ok {
		return
	}
	trade, found := c.tradeOf(orderNo)
	if !found {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "trade not found"})
		return
	}
	if next == tradeClosed && trade.Status != tradeWait {
		ctx.JSON(http.StatusOK, gin.H{"status": trade.Status, "frozen_fen": 0})
		return
	}
	if (next == tradeRefund || next == tradeChargeback) && trade.Status != tradeSuccess {
		ctx.JSON(http.StatusConflict, gin.H{"error": "trade is not paid"})
		return
	}
	trade.Status = next
	c.remember(trade)
	ctx.JSON(http.StatusOK, gin.H{"status": next, "frozen_fen": 0})
}

func (c *Cashier) signedOrderNo(ctx *gin.Context) (string, bool) {
	body, err := io.ReadAll(ctx.Request.Body)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "read body failed"})
		return "", false
	}
	var req struct {
		OrderNo string `json:"order_no"`
	}
	if json.Unmarshal(body, &req) != nil || req.OrderNo == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "order_no is required"})
		return "", false
	}
	if !hmacEqual(signBody(c.secret, []byte(req.OrderNo)), ctx.GetHeader("X-Pay-Signature")) {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return "", false
	}
	return req.OrderNo, true
}

func (c *Cashier) enqueueNotify(q url.Values, paid bool, action string) {
	if c.db == nil {
		return
	}
	next := c.now().Add(2 * time.Second)
	_ = c.db.Model(&GatewayTrade{}).Where("order_no = ?", q.Get("order_no")).Updates(map[string]any{
		"notify_url":     q.Get("notify_url"),
		"notify_action":  action,
		"notify_paid":    paid,
		"notify_status":  "pending",
		"next_notify_at": next,
	}).Error
}

func (c *Cashier) RetryNotifies() {
	if c.db == nil {
		return
	}
	var rows []GatewayTrade
	now := c.now()
	if err := c.db.Where("notify_status = ? AND notify_attempts < ? AND (next_notify_at IS NULL OR next_notify_at <= ?)", "pending", 20, now).Find(&rows).Error; err != nil {
		return
	}
	for _, row := range rows {
		q := url.Values{}
		q.Set("order_no", row.OrderNo)
		q.Set("amount_fen", strconv.FormatInt(row.AmountFen, 10))
		q.Set("notify_url", row.NotifyURL)
		err := c.notify(q, row.NotifyPaid, row.NotifyAction)
		updates := map[string]any{"notify_attempts": row.NotifyAttempts + 1}
		if err == nil {
			updates["notify_status"] = "sent"
		} else {
			again := now.Add(5 * time.Second)
			updates["next_notify_at"] = again
		}
		_ = c.db.Model(&GatewayTrade{}).Where("order_no = ?", row.OrderNo).Updates(updates).Error
	}
}

func (c *Cashier) notify(q url.Values, paid bool, action string) error {
	amount, _ := strconv.ParseInt(q.Get("amount_fen"), 10, 64)
	prefix := "T"
	if !paid {
		prefix = "F"
	}
	nonce, err := newToken(6)
	if err != nil {
		return err
	}
	body, sig, err := buildCallback(c.secret, CallbackBody{
		OrderNo:        q.Get("order_no"),
		ChannelTradeNo: prefix + q.Get("order_no"),
		AmountFen:      amount,
		Paid:           paid,
		Timestamp:      c.now().Unix(),
		Nonce:          nonce,
	})
	if err != nil {
		return err
	}
	var last error
	for i := 0; i < 3; i++ {
		req, err := http.NewRequest(http.MethodPost, q.Get("notify_url"), bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Pay-Signature", sig)
		req.Header.Set("X-Pay-Action", action)
		resp, err := c.client.Do(req)
		if err != nil {
			last = err
			continue
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		last = fmt.Errorf("商户返回 %d %s", resp.StatusCode, string(respBody))
	}
	return last
}

func notifyDelay() time.Duration {
	v := strings.TrimSpace(os.Getenv("PAY_NOTIFY_DELAY_MS"))
	if v == "" {
		return 800 * time.Millisecond
	}
	ms, err := strconv.Atoi(v)
	if err != nil || ms < 0 {
		return 800 * time.Millisecond
	}
	return time.Duration(ms) * time.Millisecond
}

func cloneValues(q url.Values) url.Values {
	out := url.Values{}
	for k, vals := range q {
		for _, v := range vals {
			out.Add(k, v)
		}
	}
	return out
}

func digitsOnly(v string) string {
	var b strings.Builder
	for _, r := range v {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func hmacEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

type tradeQueryBody struct {
	OrderNo        string `json:"order_no"`
	TradeStatus    string `json:"trade_status"`
	AmountFen      int64  `json:"amount_fen"`
	ChannelTradeNo string `json:"channel_trade_no"`
	Channel        string `json:"channel"`
}

func renderProcessing(returnURL string, waitMs int) string {
	return fmt.Sprintf(`<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="%g;url=%s"><title>支付处理中</title><body style="font-family:sans-serif;background:#f6f7f9;padding:48px"><h1>支付处理中</h1><p>渠道正在通知商户，请稍候。到账以回调或查单为准。</p></body>`, float64(waitMs)/1000, template.HTMLEscapeString(returnURL))
}

func validSandboxPassword(v string) bool {
	if len(v) != 6 {
		return false
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func renderMessage(title, detail string) string {
	return fmt.Sprintf(`<!doctype html><meta charset="utf-8"><title>%s</title><body style="font-family:sans-serif;padding:40px"><h1>%s</h1><p>%s</p></body>`, template.HTMLEscapeString(title), template.HTMLEscapeString(title), template.HTMLEscapeString(detail))
}

func renderCashier(q url.Values) string {
	amount, _ := strconv.ParseInt(q.Get("amount_fen"), 10, 64)
	coins := q.Get("coins")
	channel := q.Get("channel")
	name := map[string]string{ChannelAlipay: "支付宝沙箱", ChannelWechat: "微信支付沙箱", ChannelCard: "银行卡沙箱", ChannelMock: "沙箱支付"}[channel]
	if name == "" {
		name = "沙箱支付"
	}
	color := map[string]string{ChannelAlipay: "#1677ff", ChannelWechat: "#07c160", ChannelCard: "#111827", ChannelMock: "#1677ff"}[channel]
	data := cashierView{
		Title:     name,
		Color:     color,
		Subject:   q.Get("subject"),
		OrderNo:   q.Get("order_no"),
		Amount:    fmt.Sprintf("%.2f", float64(amount)/100),
		Coins:     coins,
		ExpireAt:  q.Get("expire_at"),
		Channel:   channel,
		AmountFen: q.Get("amount_fen"),
		NotifyURL: q.Get("notify_url"),
		ReturnURL: q.Get("return_url"),
		Sign:      q.Get("sign"),
		QR:        qrBlocks(q.Get("order_no")),
		IsCard:    channel == ChannelCard,
		IsWechat:  channel == ChannelWechat,
	}
	var buf bytes.Buffer
	_ = cashierTmpl.Execute(&buf, data)
	return buf.String()
}

type cashierView struct {
	Title, Color, Subject, OrderNo, Amount, Coins, ExpireAt, Channel, AmountFen, NotifyURL, ReturnURL, Sign string
	QR                                                                                                      template.HTML
	IsCard, IsWechat                                                                                        bool
}

func qrBlocks(seed string) template.HTML {
	sum := sha256.Sum256([]byte(seed))
	var b strings.Builder
	b.WriteString(`<div class="qr" aria-hidden="true">`)
	for i := 0; i < 121; i++ {
		on := sum[i%32]&(1<<uint(i%8)) != 0
		row, col := i/11, i%11
		if (row < 3 && col < 3) || (row < 3 && col > 7) || (row > 7 && col < 3) {
			on = (row+col)%2 == 0
		}
		if on {
			b.WriteString(`<i class="on"></i>`)
		} else {
			b.WriteString(`<i></i>`)
		}
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}

var cashierTmpl = template.Must(template.New("cashier").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>{{.Title}}</title>
  <style>
    body { margin: 0; font-family: "Segoe UI", sans-serif; background: #f4f6f8; color: #111; }
    .top { background: {{.Color}}; color: #fff; padding: 28px 20px 48px; }
    .brand { font-size: 14px; opacity: .85; }
    .amount { font-size: 42px; font-weight: 700; margin-top: 8px; }
    .card { max-width: 420px; margin: -28px auto 0; background: #fff; border-radius: 16px; padding: 20px; box-shadow: 0 10px 30px rgba(0,0,0,.08); }
    .row { display: flex; justify-content: space-between; gap: 12px; padding: 10px 0; border-bottom: 1px solid #eee; font-size: 14px; }
    label { display: block; margin: 16px 0 8px; font-size: 13px; color: #666; }
    input { width: 100%; box-sizing: border-box; font-size: 18px; padding: 12px; border: 1px solid #ddd; border-radius: 12px; }
    input.pin { font-size: 22px; letter-spacing: 8px; }
    .qr { width: 132px; display: grid; grid-template-columns: repeat(11, 1fr); gap: 2px; background: #fff; padding: 8px; border: 1px solid #eee; }
    .qr i { display: block; aspect-ratio: 1; background: #fff; }
    .qr i.on { background: #111; }
    .phone { margin-top: 12px; background: #f7f7f7; border-radius: 16px; padding: 12px; }
    .actions { display: flex; gap: 8px; margin-top: 16px; }
    button { flex: 1; border: 0; border-radius: 12px; padding: 12px; font-size: 15px; cursor: pointer; }
    .ok { background: {{.Color}}; color: #fff; }
    .bad { background: #fff; color: #c2410c; border: 1px solid #fed7aa; }
    .hint { color: #888; font-size: 12px; margin-top: 12px; }
  </style>
</head>
<body>
  <div class="top">
    <div class="brand">{{.Title}} · 收款方 ShortVideo</div>
    <div class="amount">¥ {{.Amount}}</div>
  </div>
  <form class="card" method="post" action="/cashier/submit">
    <div class="row"><span>商品</span><span>{{.Subject}}</span></div>
    <div class="row"><span>订单号</span><span>{{.OrderNo}}</span></div>
    <div class="row"><span>到账金币</span><span>{{.Coins}}</span></div>
    <div class="row"><span>剩余时间</span><span id="left">--</span></div>
    {{if .IsCard}}
    <label>测试卡号</label>
    <input name="card_number" inputmode="numeric" autocomplete="off" placeholder="4242 4242 4242 4242" />
    <label>有效期</label>
    <input name="card_exp" autocomplete="off" placeholder="12/30" />
    <label>CVV</label>
    <input name="card_cvv" inputmode="numeric" maxlength="4" autocomplete="off" placeholder="123" />
    {{else if .IsWechat}}
    <div class="phone">
      {{.QR}}
      <p class="hint">沙箱二维码不会调起真实微信。在下方输入微信支付密码完成确认。</p>
    </div>
    <label>微信支付密码</label>
    <input class="pin" name="sandbox_password" inputmode="numeric" maxlength="6" placeholder="111111" autocomplete="off" />
    {{else}}
    <div class="row"><span>沙箱买家</span><span>buyer@sandbox.shortvideo</span></div>
    <label>支付密码</label>
    <input class="pin" name="sandbox_password" inputmode="numeric" maxlength="6" placeholder="111111" autocomplete="off" />
    {{end}}
    <input type="hidden" name="order_no" value="{{.OrderNo}}" />
    <input type="hidden" name="amount_fen" value="{{.AmountFen}}" />
    <input type="hidden" name="coins" value="{{.Coins}}" />
    <input type="hidden" name="channel" value="{{.Channel}}" />
    <input type="hidden" name="subject" value="{{.Subject}}" />
    <input type="hidden" name="notify_url" value="{{.NotifyURL}}" />
    <input type="hidden" name="return_url" value="{{.ReturnURL}}" />
    <input type="hidden" name="expire_at" value="{{.ExpireAt}}" />
    <input type="hidden" name="sign" value="{{.Sign}}" />
    <div class="actions">
      <button class="ok" type="submit" name="action" value="success">确认支付</button>
      <button class="bad" type="submit" name="action" value="fail">支付失败</button>
      <button class="bad" type="submit" name="action" value="cancel">取消</button>
    </div>
    <p class="hint">沙箱环境，不会扣真实银行卡。支付宝/微信密码 111111。成功测试卡 4242424242424242，拒绝测试卡 4000000000000002。确认后渠道异步回调，商户查单可补入账。</p>
  </form>
  <script>
    const expire = Number({{.ExpireAt}}) * 1000
    const el = document.getElementById('left')
    function tick() {
      const left = Math.max(0, expire - Date.now())
      const m = String(Math.floor(left / 60000)).padStart(2, '0')
      const s = String(Math.floor((left % 60000) / 1000)).padStart(2, '0')
      el.textContent = m + ':' + s
    }
    tick()
    setInterval(tick, 1000)
  </script>
</body>
</html>`))
