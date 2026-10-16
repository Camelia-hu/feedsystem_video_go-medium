<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { ApiError } from '../api/client'
import * as payApi from '../api/pay'
import type { PayOrder } from '../api/pay'
import AppShell from '../components/AppShell.vue'
import { useAuthStore } from '../stores/auth'
import { useToastStore } from '../stores/toast'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const toast = useToastStore()

const order = ref<PayOrder | null>(null)
const error = ref('')
const waiting = ref(true)
let timer = 0

const orderNo = computed(() => (typeof route.query.order_no === 'string' ? route.query.order_no : ''))

async function poll() {
  if (!orderNo.value) {
    error.value = '缺少订单号'
    waiting.value = false
    return
  }
  try {
    const res = await payApi.queryRecharge(orderNo.value)
    order.value = res.order
    if (res.order.status === 'paid') {
      waiting.value = false
      toast.success(`支付成功，到账 ${res.order.coins} 金币`)
      window.clearInterval(timer)
    } else if (res.order.status === 'failed' || res.order.status === 'closed') {
      waiting.value = false
      window.clearInterval(timer)
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : String(e)
  }
}

onMounted(async () => {
  if (!auth.isLoggedIn) {
    await router.push('/account')
    return
  }
  await poll()
  timer = window.setInterval(poll, 1000)
})

onUnmounted(() => window.clearInterval(timer))
</script>

<template>
  <AppShell>
    <h2 class="title">支付结果</h2>
    <div class="subtle">到账以渠道回调为准。回调如果晚到，查询时会向网关查单并补入账。</div>
    <div class="card" style="margin-top: 14px; max-width: 520px">
      <div v-if="error" class="muted">{{ error }}</div>
      <template v-else-if="order">
        <div class="row" style="justify-content: space-between"><span class="subtle">订单号</span><span>{{ order.order_no }}</span></div>
        <div class="row" style="justify-content: space-between"><span class="subtle">渠道</span><span>{{ order.channel }}</span></div>
        <div class="row" style="justify-content: space-between"><span class="subtle">金额</span><span>¥ {{ payApi.fenToYuan(order.amount_fen) }}</span></div>
        <div class="row" style="justify-content: space-between"><span class="subtle">状态</span><span>{{ order.status }}</span></div>
        <p v-if="waiting" class="muted">正在等待支付网关回调…</p>
        <p v-else-if="order.status === 'paid'" class="muted">金币已入账。</p>
        <p v-else class="muted">这笔订单没有支付成功，可以重新发起充值。</p>
        <div class="row" style="margin-top: 12px">
          <button class="primary" type="button" @click="router.push('/wallet')">返回钱包</button>
        </div>
      </template>
      <div v-else class="muted">查询订单…</div>
    </div>
  </AppShell>
</template>
