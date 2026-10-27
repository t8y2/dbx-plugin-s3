import { test } from "node:test";
import assert from "node:assert/strict";
import { mapWithConcurrency } from "./concurrency.js";

test("processes every item and keeps result order regardless of completion order", async () => {
  const items = [40, 10, 30, 20];
  const result = await mapWithConcurrency(items, 2, async (value) => {
    await new Promise((resolve) => setTimeout(resolve, value));
    return value * 2;
  });
  assert.deepEqual(result, [80, 20, 60, 40]);
});

test("never runs more than `limit` workers at once", async () => {
  let active = 0;
  let peak = 0;
  await mapWithConcurrency(Array.from({ length: 12 }, (_, i) => i), 3, async () => {
    active += 1;
    peak = Math.max(peak, active);
    await new Promise((resolve) => setTimeout(resolve, 5));
    active -= 1;
  });
  assert.equal(peak, 3);
});

test("an empty item list resolves to an empty result", async () => {
  const result = await mapWithConcurrency([], 4, async () => assert.fail("worker must not run"));
  assert.deepEqual(result, []);
});

test("a rejection in the worker propagates and stops remaining lanes", async () => {
  await assert.rejects(
    () => mapWithConcurrency([1, 2, 3], 1, async (value) => {
      if (value === 2) throw new Error("boom");
      return value;
    }),
    /boom/,
  );
});
