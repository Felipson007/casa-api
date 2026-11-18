import { healthOnly } from './atlas.js';

export const options = {
  vus: 1,
  iterations: 1,
  thresholds: { http_req_failed: ['rate==0'], http_req_duration: ['p(99)<1000'] },
};

export default healthOnly;
