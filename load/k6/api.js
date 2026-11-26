import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Trend } from 'k6/metrics';
import exec from 'k6/execution';

const base = __ENV.BASE_URL || 'http://localhost:8080';
const aliceUser = __ENV.K6_USER || 'alice';
const alicePass = __ENV.K6_PASSWORD || 'password123';
const carolUser = __ENV.K6_CAROL_USER || 'carol';
const carolPass = __ENV.K6_CAROL_PASSWORD || 'password123';
const adminUser = __ENV.K6_ADMIN_USER || 'admin';
const adminPass = __ENV.K6_ADMIN_PASSWORD || '';

const jobsPerLane = 8;
const levelSeconds = 60;
const upSQL = 'SELECT pg_sleep(0.2)';
const downSQL = 'SELECT 1';

const levels = [
  { name: '5-up', vus: 5, start: '0s' },
  { name: '80-up', vus: 80, start: '1m' },
  { name: '200-up', vus: 200, start: '2m' },
  { name: '80-down', vus: 80, start: '3m' },
  { name: '5-down', vus: 5, start: '4m' },
];
const lanes = ['serialized', 'parallel'];

const readRequests = new Counter('read_requests');
const jobQueueWait = new Trend('job_queue_wait_ms');
const jobDuration = new Trend('job_duration_ms');
const jobSuccess = new Counter('job_success');
const jobFailed = new Counter('job_failed');

function readScenario(level) {
  return {
    executor: 'ramping-vus',
    exec: 'read',
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

function jobScenario(level, lane) {
  return {
    executor: 'shared-iterations',
    exec: 'runLane',
    vus: 1,
    iterations: 1,
    maxDuration: '70s',
    startTime: level.start,
    gracefulStop: '15s',
    tags: { level: level.name, lane },
  };
}

const thresholds = {
  http_req_failed: ['rate<0.01'],
  'http_req_duration{kind:read}': ['p(95)<1000'],
};
const scenarios = {};
for (const level of levels) {
  scenarios[`read-${level.name}`] = readScenario(level);
  thresholds[`http_req_failed{level:${level.name}}`] = ['rate<0.01'];
  thresholds[`http_req_duration{kind:read,level:${level.name}}`] = ['p(95)<1000'];
  thresholds[`read_requests{level:${level.name}}`] = ['count>=0'];
  for (const lane of lanes) {
    scenarios[`${lane}-${level.name}`] = jobScenario(level, lane);
    const tags = `lane:${lane},level:${level.name}`;
    thresholds[`job_queue_wait_ms{${tags}}`] = ['p(50)>=0'];
    thresholds[`job_duration_ms{${tags}}`] = ['p(50)>=0'];
    thresholds[`job_success{${tags}}`] = ['count>=0'];
    thresholds[`job_failed{${tags}}`] = ['count>=0'];
  }
}

export const options = {
  scenarios,
  thresholds,
  setupTimeout: '15m',
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(50)', 'p(90)', 'p(95)'],
  // k6 mengosongkan cookie jar di setiap iterasi. Session harus dipertahankan sendiri.
  noCookiesReset: true,
};

let sessionCookie = '';
let csrfToken = '';

function restoreSession() {
  if (sessionCookie) {
    http.cookieJar().set(base, 'dbmp_session', sessionCookie);
  }
}

function rememberSession() {
  const value = (http.cookieJar().cookiesForURL(base).dbmp_session || [])[0];
  if (value) {
    sessionCookie = value;
  }
}

export function setup() {
  if (!adminPass) {
    throw new Error('K6_ADMIN_PASSWORD kosong: isi password admin bootstrap');
  }
  const admin = login(adminUser, adminPass);
  const databases = items(admin, 'GET', '/api/admin/databases');
  const db1 = databases.find((item) => item.name === 'target-db-1');
  const db2 = databases.find((item) => item.name === 'target-db-2');
  if (!db1 || !db2) {
    throw new Error('target-db-1 atau target-db-2 tidak ada. Jalankan make seed.');
  }
  for (const db of [db1, db2]) {
    const probe = admin.send('POST', `/api/admin/databases/${db.id}/test`, {});
    if (probe.status !== 200 || probe.json('ok') !== true) {
      throw new Error(`${db.name} tidak terjangkau (cek port, sering 5434 bentrok dengan Postgres Windows): ${probe.body}`);
    }
  }
  const groups = items(admin, 'GET', '/api/admin/groups');
  const dev = groups.find((item) => item.name === 'dev');
  const analyst = groups.find((item) => item.name === 'analyst');
  if (!dev || !analyst) {
    throw new Error('group dev atau analyst tidak ada. Jalankan make seed.');
  }

  const alice = login(aliceUser, alicePass);
  const carol = login(carolUser, carolPass);
  const aliceID = must(alice.send('GET', '/api/me'), 'me alice').json('user.id');
  const carolID = must(carol.send('GET', '/api/me'), 'me carol').json('user.id');
  must(admin.send('PUT', `/api/admin/groups/${dev.id}/leader`, { user_id: aliceID }), 'leader dev');
  must(admin.send('PUT', `/api/admin/groups/${analyst.id}/leader`, { user_id: carolID }), 'leader analyst');
  must(admin.send('PUT', `/api/admin/groups/${dev.id}/access`, {
    grants: { [String(db1.id)]: 'edit', [String(db2.id)]: 'edit' },
  }), 'grant dev');

  // Kenaikan grant memutar session anggota. Masuk lagi sesudahnya.
  const editor = login(aliceUser, alicePass);
  const approver = login(carolUser, carolPass);
  const lanesByLevel = {};
  for (const level of levels) {
    lanesByLevel[level.name] = {
      serialized: approvePool(editor, approver, db1.id, level.name, 'serialized'),
      parallel: approvePool(editor, approver, db2.id, level.name, 'parallel'),
    };
  }
  return { db1: db1.id, lanes: lanesByLevel };
}

function approvePool(editor, approver, dbID, level, lane) {
  const ids = [];
  for (let n = 1; n <= jobsPerLane; n++) {
    const created = must(editor.send('POST', `/api/databases/${dbID}/migrations`, {
      title: `k6 ${level} ${lane} ${n}`,
      up_sql: upSQL,
      down_sql: downSQL,
    }), `buat migration ${level} ${lane} ${n}`);
    const migrationID = created.json('id');
    const submitted = must(editor.send('POST', `/api/databases/${dbID}/migrations/${migrationID}/submit`, {}), `submit ${migrationID}`);
    const execID = submitted.json('id');
    must(editor.send('POST', `/api/executions/${execID}/approve`, {}), `approve alice ${execID}`);
    must(approver.send('POST', `/api/executions/${execID}/approve`, {}), `approve carol ${execID}`);
    ids.push(execID);
  }
  return ids;
}

export function read(data) {
  restoreSession();
  if (!sessionCookie && !login(aliceUser, alicePass, true)) {
    sleep(0.2);
    return;
  }
  restoreSession();
  const params = { headers: { Origin: base }, tags: { kind: 'read' } };
  const me = http.get(`${base}/api/me`, params);
  readRequests.add(1);
  check(me, { 'me 200': (r) => r.status === 200 });
  const migrations = http.get(`${base}/api/databases/${data.db1}/migrations`, params);
  readRequests.add(1);
  check(migrations, { 'migrations 200': (r) => r.status === 200 });
  const executions = http.get(`${base}/api/databases/${data.db1}/executions?hide_rollback=true`, params);
  readRequests.add(1);
  check(executions, { 'executions 200': (r) => r.status === 200 });
  sleep(0.2);
}

export function runLane(data) {
  const scenario = exec.scenario.name;
  const lane = scenario.startsWith('serialized-') ? 'serialized' : 'parallel';
  const level = scenario.slice(lane.length + 1);
  const ids = (data.lanes[level] || {})[lane] || [];
  const tags = { level, lane };
  if (!login(aliceUser, alicePass, true)) {
    jobFailed.add(ids.length || 1, tags);
    return;
  }
  const accepted = [];
  for (const execID of ids) {
    const started = Date.now();
    const res = http.post(`${base}/api/executions/${execID}/enqueue`, '{}', {
      headers: authHeaders(),
      tags: { kind: 'job' },
    });
    if (res.status !== 202) {
      jobFailed.add(1, tags);
      continue;
    }
    accepted.push({ jobID: res.json('job_id'), started });
  }
  for (const job of accepted) {
    pollJob(job.jobID, job.started, tags);
  }
}

function pollJob(jobID, started, tags) {
  const deadline = Date.now() + 60000;
  while (Date.now() < deadline) {
    const res = http.get(`${base}/api/jobs/${jobID}`, {
      headers: { Origin: base },
      tags: { kind: 'job' },
    });
    const status = res.status === 200 ? res.json('status') : '';
    if (status === 'done' || status === 'dead') {
      jobDuration.add(Date.now() - started, tags);
      const wait = res.json('queue_wait_ms');
      if (typeof wait === 'number') {
        jobQueueWait.add(wait, tags);
      }
      if (res.json('execution_status') === 'success') {
        jobSuccess.add(1, tags);
      } else {
        jobFailed.add(1, tags);
      }
      return;
    }
    sleep(0.2);
  }
  jobFailed.add(1, tags);
}

function login(user, pass, keep) {
  const jar = http.cookieJar();
  jar.clear(base);
  const csrfRes = http.get(`${base}/api/csrf`, { headers: { Origin: base }, tags: { kind: 'auth' } });
  const pre = csrfRes.json('csrf_token');
  if (csrfRes.status !== 200 || !pre) {
    return null;
  }
  const res = http.post(`${base}/api/auth/login`, JSON.stringify({ username: user, password: pass }), {
    headers: {
      'Content-Type': 'application/json',
      'X-CSRF-Token': pre,
      Origin: base,
    },
    tags: { kind: 'auth' },
  });
  if (res.status !== 200) {
    if (!keep) {
      throw new Error(`login ${user}: ${res.status} ${res.body}`);
    }
    return null;
  }
  const cookie = (jar.cookiesForURL(base).dbmp_session || [])[0] || '';
  const client = {
    csrf: res.json('csrf_token'),
    cookie,
    send(method, path, body) {
      jar.set(base, 'dbmp_session', cookie);
      const headers = { Origin: base, 'X-CSRF-Token': this.csrf };
      let payload = null;
      if (body !== undefined) {
        headers['Content-Type'] = 'application/json';
        payload = JSON.stringify(body);
      }
      return http.request(method, `${base}${path}`, payload, { headers, tags: { kind: 'setup' } });
    },
  };
  if (keep) {
    sessionCookie = cookie;
    csrfToken = res.json('csrf_token') || '';
  }
  return client;
}

function authHeaders() {
  restoreSession();
  return {
    Origin: base,
    'Content-Type': 'application/json',
    'X-CSRF-Token': csrfToken,
  };
}

function items(client, method, path) {
  const res = must(client.send(method, path), path);
  return res.json('items') || [];
}

function must(res, what) {
  if (!res || res.status < 200 || res.status >= 300) {
    throw new Error(`${what}: ${res ? res.status : 'no response'} ${res ? res.body : ''}`);
  }
  return res;
}

function metricFor(data, name, parts) {
  const exact = data.metrics[`${name}{${parts.join(',')}}`];
  if (exact) {
    return exact;
  }
  for (const key of Object.keys(data.metrics)) {
    if (!key.startsWith(`${name}{`)) {
      continue;
    }
    if (parts.every((part) => key.includes(part))) {
      return data.metrics[key];
    }
  }
  return undefined;
}

function num(metric, field) {
  if (!metric || !metric.values || metric.values[field] === undefined) {
    return 'n/a';
  }
  const value = metric.values[field];
  return field === 'count' ? String(value) : value.toFixed(2);
}

function p50(metric) {
  if (!metric || !metric.values) {
    return 'n/a';
  }
  const value = metric.values['p(50)'] !== undefined ? metric.values['p(50)'] : metric.values.med;
  return value === undefined ? 'n/a' : value.toFixed(2);
}

export function handleSummary(data) {
  const lines = ['level\tvus\treads\trps\tp50_ms\tp95_ms\terror_rate'];
  for (const level of levels) {
    const duration = data.metrics[`http_req_duration{kind:read,level:${level.name}}`] || metricFor(data, 'http_req_duration', [`kind:read`, `level:${level.name}`]);
    const failed = data.metrics[`http_req_failed{level:${level.name}}`] || metricFor(data, 'http_req_failed', [`level:${level.name}`]);
    const reqs = data.metrics[`read_requests{level:${level.name}}`] || metricFor(data, 'read_requests', [`level:${level.name}`]);
    const count = reqs && reqs.values ? reqs.values.count : undefined;
    const rps = count === undefined ? 'n/a' : (count / levelSeconds).toFixed(2);
    lines.push([
      level.name,
      level.vus,
      count === undefined ? 'n/a' : count,
      rps,
      p50(duration),
      num(duration, 'p(95)'),
      num(failed, 'rate'),
    ].join('\t'));
    for (const lane of lanes) {
      const parts = [`lane:${lane}`, `level:${level.name}`];
      const wait = metricFor(data, 'job_queue_wait_ms', parts);
      const dur = metricFor(data, 'job_duration_ms', parts);
      const ok = metricFor(data, 'job_success', parts);
      const bad = metricFor(data, 'job_failed', parts);
      const finished = ((ok && ok.values && ok.values.count) || 0) + ((bad && bad.values && bad.values.count) || 0);
      lines.push([
        `  ${lane}`,
        '',
        `queue_p50=${p50(wait)}`,
        `job_p50=${p50(dur)}`,
        `jobs_per_min=${finished}`,
        `success=${num(ok, 'count')}`,
        `failed=${num(bad, 'count')}`,
      ].join('\t'));
    }
  }
  const overall = data.metrics['http_req_duration{kind:read}'];
  const overallFail = data.metrics.http_req_failed;
  lines.push(['all', '', '', '', p50(overall), num(overall, 'p(95)'), num(overallFail, 'rate')].join('\t'));
  return { stdout: `${lines.join('\n')}\n` };
}
