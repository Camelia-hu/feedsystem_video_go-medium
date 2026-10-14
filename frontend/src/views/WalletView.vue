<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import { ApiError } from '../api/client'
import * as payApi from '../api/pay'
import type { CoinPackage, LedgerEntry, Wallet } from '../api/pay'
import AppShell from '../components/AppShell.vue'
import { useAuthStore } from '../stores/auth'
import { useToastStore } from '../stores/toast'

const auth = useAuthStore()
const toast = useToastStore()
const router = useRouter()

const loading = ref(false)
const payingCode = ref('')
const channel = ref<payApi.PayChannel>('alipay')
const packages = ref<CoinPackage[]>([])
const wallet = ref<Wallet | null>(null)
const entries = ref<LedgerEntry[]>([])
const book = ref<payApi.PlatformBook | null>(null)
const orders = ref<payApi.PayOrder[]>([])
const withdrawRows = ref<{ withdraw_no: string; diamonds: number; amount_fen: number; status: string }[]>([])
const withdrawAmount = ref(10)
const settleAfter = ref(60)

function yuan(fen: number) {
  return payApi.fenToYuan(fen)
}

function statusText(status: string) {
  if (status === 'credit') return '入账'
  if (status === 'debit') return '出账'
  return status
}

async function load() {
  loading.value = true
  try {
    const pkgRes = await payApi.listPackages()
    packages.value = pkgRes.packages ?? []
    if (!auth.isLoggedIn) {
      wallet.value = null
      entries.value = []
      return
    }
    const [walletRes, ledgerRes, bookRes, orderRes, withdrawRes] = await Promise.all([
      payApi.wallet(),
      payApi.ledger({ limit: 20 }),
      payApi.books(),
      payApi.myOrders(),
      payApi.withdraws(),
    ])
    wallet.value = walletRes.wallet
    entries.value = ledgerRes.entries ?? []
    book.value = bookRes.book
    settleAfter.value = bookRes.settle_after_seconds
    orders.value = orderRes.orders ?? []
    withdrawRows.value = withdrawRes.withdraws ?? []
  } catch (e) {
    toast.error(e instanceof ApiError ? e.message : String(e))
  } finally {
    loading.value = false
  }
}

async function requestWithdraw() {
  try {
    await payApi.withdraw(withdrawAmount.value)
    toast.success('提现已受理，约 15 秒后沙箱打款')
    await load()
  } catch (e) {
    toast.error(e instanceof ApiError ? e.message : String(e))
  }
}

async function act(kind: 'refund' | 'chargeback', orderNo: string) {
  try {
    if (kind === 'refund') await payApi.refund(orderNo)
    else await payApi.chargeback(orderNo)
    toast.success(kind === 'refund' ? '已原路退款' : '已模拟拒付')
    await load()
  } catch (e) {
    toast.error(e instanceof ApiError ? e.message : String(e))
  }
}

async function buy(item: CoinPackage) {
  if (!auth.isLoggedIn) {
    await router.push('/account')
    return
  }
  payingCode.value = item.code
  try {
    const key = crypto.randomUUID()
    const res = await payApi.createRecharge({ package_code: item.code, idempotency_key: key, channel: channel.value })
    if (!res.pay_url) {
      await router.push({ path: '/pay/result', query: { order_no: res.order.order_no } })
      return
    }
    window.location.assign(res.pay_url)
  } catch (e) {
    toast.error(e instanceof ApiError ? e.message : String(e))
  } finally {
    payingCode.value = ''
  }
}

onMounted(load)
</script>

<template>
  <AppShell>
    <div class="page-head">
      <div>
        <h2 class="title">金币钱包</h2>
        <div class="subtle">跳转沙箱收银台。支付宝/微信密码 111111。成功测试卡 4242 4242 4242 4242，拒绝卡 4000 0000 0000 0002。</div>
      </div>
      <button type="button" :disabled="loading" @click="load">刷新</button>
    </div>

    <div class="card section">
      <div v-if="!auth.isLoggedIn" class="muted">登录后可以查看余额和充值。</div>
      <div v-else class="stat-grid">
        <div>
          <div class="subtle">可用金币</div>
          <div class="balance">{{ wallet?.coin_available ?? 0 }}</div>
        </div>
        <div>
          <div class="subtle">冻结金币</div>
          <div class="balance">{{ wallet?.coin_frozen ?? 0 }}</div>
        </div>
        <div>
          <div class="subtle">可用钻石</div>
          <div class="balance">{{ wallet?.diamond_available ?? 0 }}</div>
        </div>
        <div>
          <div class="subtle">冻结钻石</div>
          <div class="balance">{{ wallet?.diamond_frozen ?? 0 }}</div>
        </div>
      </div>
    </div>

    <div v-if="auth.isLoggedIn" class="card section">
      <div class="title">钻石提现</div>
      <div class="subtle">1 钻石 = ¥0.10。最低 10 钻。申请后冻结，约 15 秒沙箱打款。</div>
      <div class="withdraw-row">
        <input v-model.number="withdrawAmount" type="number" min="10" />
        <button class="primary" type="button" @click="requestWithdraw">申请提现</button>
      </div>
      <div v-for="row in withdrawRows" :key="row.withdraw_no" class="subtle">{{ row.withdraw_no }} · {{ row.diamonds }} 钻 · {{ row.status }}</div>
    </div>

    <div class="card section">
      <div class="subtle">支付方式</div>
      <div class="channel-grid">
        <button type="button" :class="{ primary: channel === 'alipay' }" @click="channel = 'alipay'">支付宝</button>
        <button type="button" :class="{ primary: channel === 'wechat' }" @click="channel = 'wechat'">微信支付</button>
        <button type="button" :class="{ primary: channel === 'card' }" @click="channel = 'card'">银行卡</button>
      </div>
    </div>

    <div class="pack-grid">
      <div v-for="item in packages" :key="item.code" class="card pack">
        <div class="title">{{ item.title }}</div>
        <div class="subtle">到账 {{ item.coins + item.bonus_coins }} 金币</div>
        <div class="bonus" :class="{ empty: !item.bonus_coins }">{{ item.bonus_coins ? `含加赠 ${item.bonus_coins}` : '无加赠' }}</div>
        <div class="price">¥ {{ yuan(item.price_fen) }}</div>
        <button class="primary" type="button" :disabled="payingCode === item.code" @click="buy(item)">
          {{ payingCode === item.code ? '创建订单…' : '充值' }}
        </button>
      </div>
    </div>

    <div v-if="auth.isLoggedIn" class="card section">
      <div class="title">商户资金</div>
      <div class="subtle">支付成功后先进入待结算，约 {{ settleAfter }} 秒后打入商户账户。手续费按 0.6% 记。</div>
      <div class="stat-grid funds">
        <div><div class="subtle">待结算</div><div class="balance">¥ {{ yuan(book?.pending_fen ?? 0) }}</div></div>
        <div><div class="subtle">已结算</div><div class="balance">¥ {{ yuan(book?.settled_fen ?? 0) }}</div></div>
        <div><div class="subtle">渠道手续费</div><div class="balance">¥ {{ yuan(book?.fee_fen ?? 0) }}</div></div>
      </div>
    </div>

    <div v-if="auth.isLoggedIn" class="card section">
      <div class="title">充值订单</div>
      <div v-if="orders.length === 0" class="muted">还没有充值订单。</div>
      <div v-for="order in orders" :key="order.order_no" class="ledger-row">
        <div>
          <div>{{ order.order_no }}</div>
          <div class="subtle">{{ order.status }} · 结算 {{ order.settle_status || 'none' }} · ¥ {{ yuan(order.amount_fen) }}</div>
        </div>
        <div class="row" v-if="order.status === 'paid'">
          <button type="button" @click="act('refund', order.order_no)">原路退款</button>
          <button type="button" @click="act('chargeback', order.order_no)">模拟拒付</button>
        </div>
      </div>
    </div>

    <div class="card section">
      <div class="title">账单</div>
      <div v-if="!auth.isLoggedIn" class="muted">登录后显示。</div>
      <div v-else-if="entries.length === 0" class="muted">还没有入账记录。</div>
      <div v-else class="ledger">
        <div v-for="entry in entries" :key="entry.id" class="ledger-row">
          <div>
            <div>{{ statusText(entry.direction) }} · {{ entry.biz_type }}</div>
            <div class="subtle">{{ entry.biz_no }} · {{ new Date(entry.created_at).toLocaleString() }}</div>
          </div>
          <div class="mono">+{{ entry.amount }} → {{ entry.balance_after }}</div>
        </div>
      </div>
    </div>
  </AppShell>
</template>

<style scoped>
.page-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.page-head button {
  flex: 0 0 auto;
}

.section {
  margin-top: 14px;
}

.stat-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}

.stat-grid.funds {
  grid-template-columns: repeat(3, minmax(0, 1fr));
  margin-top: 12px;
}

.channel-grid,
.pack-grid {
  display: grid;
  gap: 12px;
}

.channel-grid {
  grid-template-columns: repeat(3, minmax(0, 1fr));
  margin-top: 10px;
}

.channel-grid button,
.pack button,
.withdraw-row button {
  width: 100%;
}

.pack-grid {
  grid-template-columns: repeat(3, minmax(0, 1fr));
  margin-top: 14px;
  align-items: stretch;
}

.pack-grid .card + .card {
  margin-top: 0;
}

.pack {
  display: flex;
  flex-direction: column;
  gap: 8px;
  height: 100%;
}

.pack button {
  margin-top: auto;
}

.balance {
  font-size: 28px;
  font-weight: 700;
  margin-top: 4px;
  font-variant-numeric: tabular-nums;
}

.price {
  font-size: 22px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}

.bonus {
  min-height: 20px;
  color: #25f4ee;
  font-size: 13px;
}

.bonus.empty {
  color: var(--muted);
}

.withdraw-row {
  display: grid;
  grid-template-columns: 160px 140px;
  gap: 10px;
  margin-top: 10px;
  align-items: center;
}

.ledger {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.ledger-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 12px;
  padding-top: 10px;
  border-top: 1px solid rgba(255, 255, 255, 0.08);
}

.mono {
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

@media (max-width: 900px) {
  .stat-grid,
  .stat-grid.funds,
  .channel-grid,
  .pack-grid {
    grid-template-columns: 1fr;
  }

  .withdraw-row {
    grid-template-columns: 1fr;
  }
}
</style>
