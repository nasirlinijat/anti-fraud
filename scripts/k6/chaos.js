import http from 'k6/http';
import { sleep } from 'k6';
import { SharedArray } from 'k6/data';
import { Counter } from 'k6/metrics';
import exec from 'k6/execution';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const API_KEY = __ENV.API_KEY;
const RUN = __ENV.RUN || `chaos-${Date.now()}`;
const PURCHASES = __ENV.PURCHASES || './purchases.jsonl';
const GIVE_UP_AFTER_MS = Number(__ENV.GIVE_UP_AFTER_MS || 180000);
const RECHECK_SHARE = Number(__ENV.RECHECK_SHARE || 0.1);

if (!API_KEY) {
  throw new Error('set API_KEY to the service key of the target');
}

const purchases = new SharedArray('purchases', () =>
  open(PURCHASES)
    .split('\n')
    .filter((line) => line.trim() !== '')
    .map((line) => JSON.parse(line).request),
);

export const options = {
  scenarios: {
    buyers: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.RATE || 10),
      timeUnit: '1s',
      duration: __ENV.DURATION || '6m',
      preAllocatedVUs: 50,
      maxVUs: 500,
      gracefulStop: `${Math.ceil(GIVE_UP_AFTER_MS / 1000) + 30}s`,
    },
  },
  thresholds: {
    purchases_given_up: ['count==0'],
    retry_answer_changed: ['count==0'],
  },
};

const confirmed = new Counter('purchases_confirmed');
const givenUp = new Counter('purchases_given_up');
const retries = new Counter('score_retries');
const failuresByKind = new Counter('score_failures');
const rechecks = new Counter('rechecks_sent');
const changed = new Counter('retry_answer_changed');

function withRun(id) {
  return id ? id.replace(/^k6-/, `${RUN}-`) : id;
}

function purchaseFor(index) {
  const base = purchases[index % purchases.length];
  const request = Object.assign({}, base);
  const now = Date.now();
  request.transaction_id = `${RUN}-${index}`;
  request.player_id = withRun(request.player_id);
  request.card_token = withRun(request.card_token);
  request.device_id = withRun(request.device_id);
  if (request.account_created_at) {
    const age = Date.parse(base.event_time) - Date.parse(base.account_created_at);
    request.account_created_at = new Date(now - 1000 - age).toISOString();
  }
  request.event_time = new Date(now - 1000).toISOString();
  return JSON.stringify(request);
}

function send(body) {
  return http.post(`${BASE_URL}/score`, body, {
    headers: { 'Content-Type': 'application/json', 'X-API-Key': API_KEY },
    tags: { name: 'POST /score' },
    timeout: '10s',
  });
}

function failureKind(res) {
  if (res.status === 0) {
    return 'network';
  }
  return String(res.status);
}

function scoreUntilAnswered(body) {
  const started = Date.now();
  let wait = 0.2;
  for (;;) {
    const res = send(body);
    if (res.status === 200) {
      return res.json();
    }
    failuresByKind.add(1, { kind: failureKind(res) });
    if (Date.now() - started > GIVE_UP_AFTER_MS) {
      return null;
    }
    retries.add(1);
    sleep(wait + Math.random() * wait);
    wait = Math.min(wait * 2, 5);
  }
}

export default function () {
  const body = purchaseFor(exec.scenario.iterationInTest);
  const answer = scoreUntilAnswered(body);
  if (answer === null) {
    givenUp.add(1);
    return;
  }
  confirmed.add(1);

  if (Math.random() < RECHECK_SHARE) {
    rechecks.add(1);
    const again = scoreUntilAnswered(body);
    if (again === null) {
      givenUp.add(1);
      return;
    }
    if (again.outcome !== answer.outcome || again.score !== answer.score || again.ruleset_version !== answer.ruleset_version) {
      changed.add(1);
    }
  }
}
