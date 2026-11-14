import assert from 'node:assert/strict'
import { test } from 'node:test'
import { reasonLabel, reasonStrength, reasonValue } from '../src/reviews/modelReasons.ts'

test('model features get readable labels, with a fallback for new features', () => {
  assert.equal(reasonLabel('card_uses_1h'), 'Card uses in the last hour')
  assert.equal(reasonLabel('ip_country_differs_from_card'), "IP country differs from the card's country")
  assert.equal(reasonLabel('brand_new_feature'), 'Brand new feature')
})

test('feature values are shown in their unit and codes are hidden', () => {
  assert.equal(reasonValue('amount', 499.5), '$499.50')
  assert.equal(reasonValue('card_uses_1h', 6), '6')
  assert.equal(reasonValue('secs_since_last', 45), '45 s')
  assert.equal(reasonValue('secs_since_last', 5_400), '2 h')
  assert.equal(reasonValue('secs_since_first', 864_000), '10 days')
  assert.equal(reasonValue('account_age_days', 1), '1 day')
  assert.equal(reasonValue('email_domain_share', 0.125), '12.5%')
  assert.equal(reasonValue('amount_to_avg', 8.355), '8.36×')
  assert.equal(reasonValue('device_is_new_for_player', 1), 'Yes')
  assert.equal(reasonValue('has_device_info', 0), 'No')
  assert.equal(reasonValue('card_country', 37), null)
  assert.equal(reasonValue('account_age_days', -1), 'Not known')
})

test('the strongest reason gets the full bar and the others a share of it', () => {
  const reasons = [{ feature: 'a', value: 0, effect: 0.8 }, { feature: 'b', value: 0, effect: -0.2 }]
  assert.equal(reasonStrength(reasons[0], reasons), 1)
  assert.equal(reasonStrength(reasons[1], reasons), 0.25)
})
