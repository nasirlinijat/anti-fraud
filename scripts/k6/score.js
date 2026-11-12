import http from 'k6/http';
import { check } from 'k6';
import { SharedArray } from 'k6/data';
import { Counter } from 'k6/metrics';
import exec from 'k6/execution';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const API_KEY = __ENV.API_KEY;
const RUN = __ENV.RUN || `k6-${Date.now()}`;
const SCENARIO = __ENV.SCENARIO || 'steady';
const PURCHASES = __ENV.PURCHASES || './purchases.jsonl';

if (!API_KEY) {
  throw new Error('set API_KEY to the service key of the target');
}

const purchases = new SharedArray('purchases', () =>
  open(PURCHASES)
    .split('\n')
    .filter((line) => line.trim() !== '')
    .map((line) => {
      const row = JSON.parse(line);
      return { pattern: row.pattern, request: row.request };
    }),
);

const scenarios = {
  steady: {
    executor: 'constant-arrival-rate',
    rate: Number(__ENV.RATE || 20),
    timeUnit: '1s',
    duration: __ENV.DURATION || '2m',
    preAllocatedVUs: 50,
    maxVUs: 300,
  },
  ramp: {
    executor: 'ramping-arrival-rate',
    startRate: 10,
    timeUnit: '1s',
    preAllocatedVUs: 50,
    maxVUs: 1000,
    stages: rampStages(Number(__ENV.MAX_RATE || 200), Number(__ENV.STEPS || 8), __ENV.STEP || '45s'),
  },
  spike: {
    executor: 'ramping-arrival-rate',
    startRate: Number(__ENV.BASE_RATE || 10),
    timeUnit: '1s',
    preAllocatedVUs: 50,
    maxVUs: 1000,
    stages: [
      { target: Number(__ENV.BASE_RATE || 10), duration: '30s' },
      { target: Number(__ENV.SPIKE_RATE || 150), duration: '5s' },
      { target: Number(__ENV.SPIKE_RATE || 150), duration: '30s' },
      { target: Number(__ENV.BASE_RATE || 10), duration: '5s' },
      { target: Number(__ENV.BASE_RATE || 10), duration: '30s' },
    ],
  },
};

function rampStages(maxRate, steps, step) {
  const stages = [];
  for (let i = 1; i <= steps; i++) {
    const target = Math.round((maxRate * i) / steps);
    stages.push({ target, duration: '10s' });
    stages.push({ target, duration: step });
  }
  return stages;
}

if (!scenarios[SCENARIO]) {
  throw new Error(`SCENARIO must be one of ${Object.keys(scenarios).join(', ')}`);
}

export const options = {
  scenarios: { [SCENARIO]: scenarios[SCENARIO] },
  thresholds: {
    http_req_duration: [{ threshold: 'p(95)<200', abortOnFail: SCENARIO === 'ramp', delayAbortEval: '30s' }],
    http_req_failed: [{ threshold: 'rate<0.01', abortOnFail: SCENARIO === 'ramp', delayAbortEval: '30s' }],
    checks: ['rate>0.99'],
  },
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

const outcomes = new Counter('score_outcomes');

function withRun(id) {
  return id ? id.replace(/^k6-/, `${RUN}-`) : id;
}

function scoreRequest(index) {
  const base = purchases[index % purchases.length];
  const request = Object.assign({}, base.request);
  const now = Date.now();
  request.transaction_id = `${RUN}-${index}`;
  request.player_id = withRun(request.player_id);
  request.card_token = withRun(request.card_token);
  request.device_id = withRun(request.device_id);
  if (request.account_created_at) {
    const age = Date.parse(base.request.event_time) - Date.parse(base.request.account_created_at);
    request.account_created_at = new Date(now - 1000 - age).toISOString();
  }
  request.event_time = new Date(now - 1000).toISOString();
  return { pattern: base.pattern, request };
}

export default function () {
  const { pattern, request } = scoreRequest(exec.scenario.iterationInTest);
  const res = http.post(`${BASE_URL}/score`, JSON.stringify(request), {
    headers: { 'Content-Type': 'application/json', 'X-API-Key': API_KEY },
    tags: { name: 'POST /score', fraud_pattern: pattern === 'normal' ? 'normal' : 'special' },
  });
  const ok = check(res, {
    'status is 200': (r) => r.status === 200,
    'has an outcome': (r) => r.status === 200 && ['allow', 'review', 'block'].includes(r.json('outcome')),
  });
  if (ok) {
    outcomes.add(1, { outcome: res.json('outcome') });
  }
}
