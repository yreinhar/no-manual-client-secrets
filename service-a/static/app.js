async function runCall() {
  const btn = document.getElementById('btn-call');
  const stepsEl = document.getElementById('steps');
  const respBox = document.getElementById('response-box');
  const respBody = document.getElementById('response-body');
  const tokenBox = document.getElementById('token-box');
  const accessTokenBox = document.getElementById('access-token-box');
  const errMsg = document.getElementById('error-msg');

  btn.disabled = true;
  stepsEl.innerHTML = '';
  respBox.style.display = 'none';
  tokenBox.style.display = 'none';
  accessTokenBox.style.display = 'none';
  errMsg.style.display = 'none';

  try {
    const res = await fetch('/call-service-b');
    const data = await res.json();

    (data.steps || []).forEach(s => {
      const d = document.createElement('div');
      d.className = 'step ' + (s.ok ? 'ok' : 'err');
      d.textContent = (s.ok ? '✓' : '✗') + '  ' + s.n + '. ' + s.name + ' — ' + s.detail;
      stepsEl.appendChild(d);
    });

    if (data.body) {
      let pretty = data.body;
      try { pretty = JSON.stringify(JSON.parse(data.body), null, 2); } catch(_) {}
      respBody.textContent = pretty;
      respBox.style.display = 'block';
    }
  } catch (e) {
    errMsg.textContent = 'Request failed: ' + e;
    errMsg.style.display = 'block';
  } finally {
    btn.disabled = false;
  }
}

async function inspectToken() {
  const btn = document.getElementById('btn-token');
  const tokenBox = document.getElementById('token-box');
  const tokenBody = document.getElementById('token-body');
  const accessTokenBox = document.getElementById('access-token-box');
  const accessTokenRaw = document.getElementById('access-token-raw');
  const accessTokenClaims = document.getElementById('access-token-claims');
  const stepsEl = document.getElementById('steps');
  const errMsg = document.getElementById('error-msg');

  btn.disabled = true;
  stepsEl.innerHTML = '';
  tokenBox.style.display = 'none';
  accessTokenBox.style.display = 'none';
  errMsg.style.display = 'none';

  try {
    const res = await fetch('/token-info');
    const data = await res.json();
    if (data.error) {
      errMsg.textContent = 'Error: ' + data.error;
      errMsg.style.display = 'block';
    } else {
      tokenBody.textContent = JSON.stringify(data.claims, null, 2);
      tokenBox.style.display = 'block';
      if (data.kc_error) {
        errMsg.textContent = 'Keycloak error: ' + data.kc_error;
        errMsg.style.display = 'block';
      }
      if (data.access_token) {
        accessTokenRaw.textContent = data.access_token;
        accessTokenClaims.textContent = data.access_token_claims
          ? JSON.stringify(data.access_token_claims, null, 2)
          : '(could not decode)';
        accessTokenBox.style.display = 'block';
      }
    }
  } catch (e) {
    errMsg.textContent = 'Request failed: ' + e;
    errMsg.style.display = 'block';
  } finally {
    btn.disabled = false;
  }
}
