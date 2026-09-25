// Signs the Playwright MCP browser in to the GreenNode console as the IAM User
// in .env. Start `make browser-creds` first, then run this file with the
// browser_run_code_unsafe tool (filename: scripts/browser-login.js). It
// returns only the final origin. The form fields are cleared before it
// returns or throws, so a page snapshot does not show the filled values.
async (page) => {
  const helper = 'http://127.0.0.1:18765';
  const fetchJSON = async (path) => {
    const resp = await page.request.get(helper + path);
    if (!resp.ok()) {
      throw new Error(`browser-creds ${path} returned ${resp.status()}; restart make browser-creds`);
    }
    return resp.json();
  };
  const fields = [
    page.getByRole('textbox', { name: 'Root email' }),
    page.getByRole('textbox', { name: 'Username' }),
    page.getByRole('textbox', { name: 'Password' }),
  ];
  // The Playwright sandbox has no URL global.
  const urlParts = () => {
    const m = page.url().match(/^(https?:\/\/[^/?#]+)([^?#]*)/);
    return { origin: m ? m[1] : '', pathname: m ? m[2] : '' };
  };
  // Never type a credential into a page that is not the GreenNode sign-in
  // host; a redirect elsewhere would receive it.
  const assertSigninHost = () => {
    if (urlParts().origin !== 'https://signin.greennode.ai') {
      throw new Error('unexpected sign-in host ' + urlParts().origin + '; refusing to fill credentials');
    }
  };
  const clearFields = async () => {
    for (const field of fields) {
      await field.fill('', { timeout: 1000 }).catch(() => {});
    }
  };

  const creds = await fetchJSON('/creds');
  if (!creds.rootEmail || !creds.username || !creds.password) {
    throw new Error('browser-creds served empty IAM User values; check .env');
  }

  await page.goto('https://dashboard.console.greennode.ai/');
  await page.getByRole('button', { name: /sign in with iam user account/i }).first().click();
  assertSigninHost();
  try {
    await fields[0].fill(creds.rootEmail);
    await fields[1].fill(creds.username);
    await fields[2].fill(creds.password);
    await page.getByRole('button', { name: /sign in with iam user account/i }).click();
    await page.waitForLoadState('networkidle');
  } finally {
    if (urlParts().pathname.endsWith('/iam/login')) {
      await clearFields();
    }
  }

  if (urlParts().pathname.includes('/ap/auth/iam/google')) {
    const totp = await fetchJSON('/totp');
    if (!totp.code) {
      throw new Error('2FA required but VNGCLOUD_TOTP_SECRET is empty');
    }
    assertSigninHost();
    await page.locator('input:not([type=hidden])').first().fill(totp.code);
    await page.locator('button[type=submit]').first().click();
  }

  await page.waitForURL((url) => url.host === 'dashboard.console.greennode.ai', { timeout: 30000 });
  return urlParts().origin;
}
