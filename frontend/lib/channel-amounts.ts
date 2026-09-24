import type { Channel } from "@/lib/api-types"

export function rechargeMultiplier(channel: Channel): number {
  const value = channel.recharge_multiplier
  return Number.isFinite(value) && value > 0 ? value : 1
}

export function actualChannelBalance(channel: Channel): number | null {
  if (channel.balance_mode === "manual") return channel.last_balance ?? channel.manual_balance
  return channel.last_balance == null ? null : channel.last_balance * rechargeMultiplier(channel)
}
