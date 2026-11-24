import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';

const requests = new Counter('level_requests');

const base = __ENV.BASE_URL || 'http://localhost:8080';
const username = __ENV.K6_USER || 'alice';
const password = __ENV.K6_PASSWORD || 'password123';

const levels = [
  { name: '5-up', vus: 5, start: '0s' },
  { name: '15-up', vus: 15, start: '1m' },
  { name: '30-up', vus: 30, start: '2m' },
  { name: '15-down', vus: 15, start: '3m' },
  { name: '5-down', vus: 5, start: '4m' },
];

function scenario(level) {
  return {
    executor: 'ramping-vus',
    startVUs: 0,
    stages: [
      { duration: '15s', target: level.vus },
      { duration: '45s', target: level.vus },
    ],
    startTime: level.start,
    gracefulRampDown: '0s',
    gracefulStop: '0s',
    tags: { level: level.name },
  };
}

const thresholds = {
  http_req_failed: ['rate<0.01'],
  'http_req_duration{kind:read}': ['p(95)<1000'],
};
for (const level of levels) {
  thresholds[`http_req_failed{level:${level.name}}`] = ['rate<0.01'];
  thresholds[`http_req_duration{kind:read,level:${level.name}}`] = ['p(95)<1000'];
  thresholds[`level_requests{level:${level.name}}`] = ['count>=0'];
}

export const options = {
  scenarios: Object.fromEntries(levels.map((level) => [level.name, scenario(level)])),
  thresholds,
  // k6 mengosongkan cookie jar di setiap iterasi. Session harus dipertahankan sendiri.
  noCookiesReset: true,
};

let ready = false;
let dbID = 0;
let sessionCookie = '';

function rememberSession() {
  const cookies = http.cookieJar().cookiesForURL(base);
  const value = (cookies.dbmp_session || [])[0];
  if (value) {
    sessionCookie = value;
  }
}

function restoreSession() {
  if (sessionCookie) {
    http.cookieJar().set(base, 'dbmp_session', sessionCookie);
  }
}

function track(res) {
  requests.add(1);
  return res;
}

export default function () {
  restoreSession();
  if (!ready) {
    if (!login()) {
      sleep(1);
      return;
    }
    dbID = databaseID();
    if (!dbID) {
      sleep(1);
      return;
    }
    ready = true;
  }
  const params = { headers: { Origin: base }, tags: { kind: 'read' } };
  const me = track(http.get(`${base}/api/me`, params));
  check(me, { 'me 200': (r) => r.status === 200 });
  const migrations = track(http.get(`${base}/api/databases/${dbID}/migrations`, params));
  check(migrations, { 'migrations 200': (r) => r.status === 200 });
  const executions = track(http.get(`${base}/api/databases/${dbID}/executions?hide_rollback=true`, params));
  check(executions, { 'executions 200': (r) => r.status === 200 });
  sleep(1);
}

function login() {
  const csrfRes = track(http.get(`${base}/api/csrf`, {
    headers: { Origin: base },
    tags: { kind: 'auth' },
  }));
  const token = csrfRes.json('csrf_token');
  const csrfOK = check(csrfRes, {
    'csrf 200': (r) => r.status === 200 && typeof token === 'string' && token.length > 0,
  });
  if (!csrfOK) {
    return false;
  }
  const res = track(http.post(
    `${base}/api/auth/login`,
    JSON.stringify({ username, password }),
    {
      headers: {
        'Content-Type': 'application/json',
        'X-CSRF-Token': token,
        Origin: base,
      },
      tags: { kind: 'auth' },
    },
  ));
  rememberSession();
  return check(res, { 'login 200': (r) => r.status === 200 });
}

function databaseID() {
  const res = track(http.get(`${base}/api/databases`, {
    headers: { Origin: base },
    tags: { kind: 'read' },
  }));
  const items = res.json('items') || [];
  const found = items.find((item) => item.name === 'target-db-1');
  check(res, {
    'databases 200': (r) => r.status === 200 && found !== undefined,
  });
  return found ? found.id : 0;
}

function metricFor(data, name, levelName) {
  const exact = data.metrics[`${name}{level:${levelName}}`];
  if (exact) {
    return exact;
  }
  for (const key of Object.keys(data.metrics)) {
    if (key.startsWith(`${name}{`) && key.includes(`level:${levelName}`)) {
      return data.metrics[key];
    }
  }
  return undefined;
}

export function handleSummary(data) {
  const lines = ['level\tvus\trequests\tp95_read_ms\terror_rate'];
  for (const level of levels) {
    const duration = data.metrics[`http_req_duration{kind:read,level:${level.name}}`] || metricFor(data, 'http_req_duration', level.name);
    const failed = data.metrics[`http_req_failed{level:${level.name}}`] || metricFor(data, 'http_req_failed', level.name);
    const reqs = data.metrics[`level_requests{level:${level.name}}`] || metricFor(data, 'level_requests', level.name);
    const p95 = duration && duration.values ? duration.values['p(95)'] : undefined;
    const rate = failed && failed.values ? failed.values.rate : undefined;
    const count = reqs && reqs.values ? reqs.values.count : undefined;
    lines.push(
      [
        level.name,
        level.vus,
        count === undefined ? 'n/a' : count,
        p95 === undefined ? 'n/a' : p95.toFixed(2),
        rate === undefined ? 'n/a' : rate.toFixed(4),
      ].join('\t'),
    );
  }
  const overallP95 = data.metrics['http_req_duration{kind:read}'];
  const overallFail = data.metrics.http_req_failed;
  lines.push(
    [
      'all',
      '',
      '',
      overallP95 && overallP95.values ? overallP95.values['p(95)'].toFixed(2) : 'n/a',
      overallFail && overallFail.values ? overallFail.values.rate.toFixed(4) : 'n/a',
    ].join('\t'),
  );
  return { stdout: `${lines.join('\n')}\n` };
}
