import http from 'k6/http';
import { check, sleep } from 'k6';

const baseURL = (__ENV.BASE_URL || 'http://localhost:8081').replace(/\/$/, '');
const authCookie = __ENV.AUTH_COOKIE || '';
const bearer = __ENV.AUTH_BEARER || '';
const loanID = __ENV.LOAN_ID || '00000000-0000-0000-0000-000000000000';
const quick = __ENV.QUICK === '1';
const rampStages = quick
  ? [{ duration: '5s', target: 3 }, { duration: '10s', target: 8 }, { duration: '5s', target: 0 }]
  : [{ duration: '2m', target: 10 }, { duration: '3m', target: 30 }, { duration: '2m', target: 0 }];
const steadyStart = quick ? '25s' : '7m30s';
const steadyDuration = quick ? (__ENV.STEADY_DURATION || '30s') : (__ENV.STEADY_DURATION || '5m');
const spikeStart = quick ? '1m' : '13m';
const spikeVUs = Number(__ENV.SPIKE_VUS || (quick ? 20 : 100));
const year = __ENV.YEAR || '2026';
const month = __ENV.MONTH || '08';

export const options = {
  scenarios: {
    ramp_up: { executor: 'ramping-vus', startVUs: 1, stages: rampStages, startTime: '0s', exec: 'readFlows', tags: { profile: 'ramp-up' } },
    steady_state: { executor: 'constant-vus', vus: Number(__ENV.STEADY_VUS || (quick ? 5 : 20)), duration: steadyDuration, startTime: steadyStart, exec: 'readFlows', tags: { profile: 'steady' } },
    spike: { executor: 'ramping-vus', startVUs: 0, stages: [{ duration: '5s', target: spikeVUs }, { duration: quick ? '10s' : '45s', target: spikeVUs }, { duration: quick ? '5s' : '20s', target: 0 }], startTime: spikeStart, exec: 'readFlows', tags: { profile: 'spike' } },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<300', 'p(99)<750'],
    'http_req_duration{endpoint:health}': ['p(99)<100'],
    'http_req_duration{endpoint:dashboard}': ['p(95)<500', 'p(99)<1200'],
  },
};

function params(endpoint) {
  const headers = { Accept: 'application/json' };
  if (authCookie) headers.Cookie = authCookie;
  if (bearer) headers.Authorization = `Bearer ${bearer}`;
  return { headers, tags: { endpoint } };
}

function get(endpoint, path, expected = 200) {
  const response = http.get(`${baseURL}${path}`, params(endpoint));
  check(response, { [`${endpoint}: status ${expected}`]: (r) => r.status === expected });
  return response;
}

export function readFlows() {
  get('health', '/health');
  get('dashboard', `/api/dashboard/kpis?year=${year}&month=${month}`);
  get('dashboard', '/api/dashboard/resumo');
  get('loans', '/api/loans');
  get('loans', `/api/loans/vigentes?ano=${year}&mes=${month}`);
  get('transactions', '/api/transactions');
  get('expenses', '/api/expenses');
  get('expenses-summary', `/api/expenses/monthly-summary?year=${year}&month=${month}`);
  get('income', '/api/income');
  get('income-summary', `/api/income/monthly-summary?year=${year}&month=${month}`);
  get('notifications', '/api/notifications');
  if (__ENV.LOAN_ID) get('loan-detail', `/api/loans/${loanID}`);
  sleep(Number(__ENV.THINK_TIME || 0.2));
}

export function healthOnly() { get('health', '/health'); }
