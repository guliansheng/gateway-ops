import assert from "node:assert/strict"
import test from "node:test"
import { availabilityBand, availabilityBar } from "../lib/group-monitor.ts"

test("availability boundaries are inclusive and non-overlapping", () => {
  for (const [value, label] of [[1, "≥90%"], [0.9, "≥90%"], [0.8999, "70%–<90%"], [0.7, "70%–<90%"], [0.6999, "50%–<70%"], [0.5, "50%–<70%"], [0.4999, "<50%"], [0, "<50%"]]) {
    assert.equal(availabilityBand(value)?.label, label, `value ${value}`)
  }
})

test("missing or invalid ratios are not classified as failures", () => {
  for (const value of [null, undefined, NaN, Infinity, -0.1, 1.1]) assert.equal(availabilityBand(value), null)
})

test("timeline uses the same bands and falls back to successful request counts", () => {
  const bucket = { start_at: "", end_at: "", total: 10, success_count: 7, failure_count: 3, data_complete: true }
  assert.equal(availabilityBar(bucket).label, "可用率 70%–<90%")
  assert.equal(availabilityBar({ ...bucket, availability: 0.9 }).label, "可用率 ≥90%")
  assert.equal(availabilityBar({ ...bucket, availability: NaN }).label, "可用率 70%–<90%")
})

test("empty and incomplete buckets stay neutral regardless of their score", () => {
  const bucket = { start_at: "", end_at: "", total: 10, success_count: 0, failure_count: 10, data_complete: false, availability: 0 }
  assert.equal(availabilityBar(bucket).label, "数据不完整")
  assert.equal(availabilityBar({ ...bucket, data_complete: true, total: 0 }).label, "无请求")
  assert.equal(availabilityBar({ ...bucket, data_complete: true, availability: 2 }).label, "可用率未知")
})

test("availability colors use green, light green, yellow and red consistently", () => {
  for (const [value, color, textTone] of [[0.9, "bg-emerald-600", "text-emerald-700"], [0.7, "bg-emerald-300", "text-emerald-600"], [0.5, "bg-yellow-400", "text-yellow-700"], [0.49, "bg-red-500", "text-red-700"]]) {
    const band = availabilityBand(value)
    assert.ok(band.shape.includes(color))
    assert.ok(band.textTone.includes(textTone))
    assert.ok(availabilityBar({ total: 100, success_count: value * 100, failure_count: (1 - value) * 100, data_complete: true }).shape.includes(color))
  }
})
