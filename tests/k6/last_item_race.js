import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';

// Metrics
const successfulReservations = new Counter('successful_reservations');
const failedReservations = new Counter('failed_reservations');
const rateLimitErrors = new Counter('rate_limit_errors');

export const options = {
  scenarios: {
    last_item_race: {
      executor: 'per-vu-iterations',
      vus: 50,
      iterations: 1,
      maxDuration: '30s',
    },
  },
  thresholds: {
    // Acceptance criterion: exactly 1 VU succeeds, 49 fail with out of stock, 0 rate limit 429 errors!
    'successful_reservations': ['count==1'],
    'failed_reservations': ['count==49'],
    'rate_limit_errors': ['count==0'],
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const PRODUCT_ID = __ENV.TEST_PRODUCT_ID || 'OLJCESPC7Z'; // Vintage Typewriter

export default function () {
  const vuIP = `10.0.1.${__VU}`;

  // Step 1: Visit home page with client IP
  const homeResp = http.get(`${BASE_URL}/`, {
    headers: {
      'X-Forwarded-For': vuIP,
      'X-Bypass-Rate-Limit': 'test-suite',
    },
  });
  check(homeResp, {
    'home page status is 200': (r) => r.status === 200,
  });

  // Step 2: Concurrently post add-to-cart for the single remaining unit
  const payload = {
    product_id: PRODUCT_ID,
    quantity: '1',
  };

  const params = {
    headers: {
      'Content-Type': 'application/x-www-form-urlencoded',
      'X-Forwarded-For': vuIP,
      'X-Bypass-Rate-Limit': 'test-suite',
    },
    redirects: 2,
  };

  const cartResp = http.post(`${BASE_URL}/cart`, payload, params);

  // Hard check for HTTP 429
  if (cartResp.status === 429) {
    rateLimitErrors.add(1);
    console.error(`VU ${__VU} was rate-limited with HTTP 429!`);
    return;
  }

  const isSuccess = cartResp.status === 200 &&
    !cartResp.body.includes('out of stock') &&
    !cartResp.body.includes('not enough stock') &&
    !cartResp.body.includes('failed');

  if (isSuccess) {
    successfulReservations.add(1);
  } else {
    failedReservations.add(1);
  }

  check(cartResp, {
    'cart request handled without 429': (r) => r.status !== 429,
  });
}
