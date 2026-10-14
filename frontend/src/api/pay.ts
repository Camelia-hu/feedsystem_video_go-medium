import { postJson } from './client'

export type CoinPackage = {
  id: number
  code: string
  title: string
  coins: number
  bonus_coins: number
  price_fen: number
  currency: string
  status: string
  sort: number
}

export type PayOrder = {
  id: number
  order_no: string
  account_id: number
  package_code: string
  coins: number
  amount_fen: number
  currency: string
  channel: string
  status: string
  channel_trade_no?: string
  paid_after_close: boolean
  fee_fen: number
  net_fen: number
  settle_status: string
  settle_at?: string
  expire_at: string
  paid_at?: string
  created_at: string
}

export type Wallet = {
  account_id: number
  coin_available: number
  coin_frozen: number
  diamond_available: number
  diamond_frozen: number
}

export type LedgerEntry = {
  id: number
  account_id: number
  currency: string
  direction: string
  amount: number
  balance_after: number
  biz_type: string
  biz_no: string
  idempotency_key: string
  created_at: string
}

export function listPackages() {
  return postJson<{ packages: CoinPackage[] }>('/pay/packages', {})
}

export type PayChannel = 'alipay' | 'wechat' | 'card'

export function createRecharge(body: { package_code: string; idempotency_key: string; channel: PayChannel }) {
  return postJson<{ order: PayOrder; pay_url: string }>('/pay/recharge/create', body, { authRequired: true })
}

export function queryRecharge(orderNo: string) {
  return postJson<{ order: PayOrder }>('/pay/recharge/query', { order_no: orderNo }, { authRequired: true })
}

export function wallet() {
  return postJson<{ wallet: Wallet }>('/pay/wallet', {}, { authRequired: true })
}

export function ledger(body?: { before_id?: number; limit?: number }) {
  return postJson<{ entries: LedgerEntry[]; next_before?: number; has_more: boolean }>('/pay/ledger', body ?? {}, {
    authRequired: true,
  })
}

export type PlatformBook = {
  pending_fen: number
  settled_fen: number
  fee_fen: number
  refund_fen: number
  chargeback_fen: number
  coin_shortfall: number
}

export function books() {
  return postJson<{ book: PlatformBook; settle_after_seconds: number }>('/pay/books', {}, { authRequired: true })
}

export function myOrders() {
  return postJson<{ orders: PayOrder[] }>('/pay/orders', {}, { authRequired: true })
}

export function refund(orderNo: string) {
  return postJson<{ order: PayOrder }>('/pay/recharge/refund', { order_no: orderNo }, { authRequired: true })
}

export type Gift = {
  code: string
  name: string
  coin_price: number
  popularity_weight: number
}

export function giftCatalog() {
  return postJson<{ gifts: Gift[] }>('/pay/gift/catalog', {})
}

export function giftStat(videoId: number) {
  return postJson<{ stat: { video_id: number; gift_count: number; gift_coins: number } }>('/pay/gift/stat', { video_id: videoId })
}

export function sendGift(body: { video_id: number; gift_code: string; count: number; idempotency_key: string }) {
  return postJson<{ gift: { gift_no: string; coin_cost: number; diamond_credit: number }; wallet: Wallet }>('/pay/gift/send', body, {
    authRequired: true,
  })
}

export function withdraw(diamonds: number) {
  return postJson<{ withdraw: { withdraw_no: string; diamonds: number; amount_fen: number; status: string } }>('/pay/withdraw', { diamonds }, { authRequired: true })
}

export function withdraws() {
  return postJson<{ withdraws: { withdraw_no: string; diamonds: number; amount_fen: number; status: string; created_at: string }[] }>('/pay/withdraws', {}, { authRequired: true })
}

export function chargeback(orderNo: string) {
  return postJson<{ order: PayOrder }>('/pay/recharge/chargeback', { order_no: orderNo }, { authRequired: true })
}

export function fenToYuan(fen: number) {
  return (fen / 100).toFixed(2)
}
