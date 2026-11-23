async (page) => {
  const helper = "__VNGCLOUD_BROWSER_HELPER__";
  const capability = "__VNGCLOUD_BROWSER_CAPABILITY__";
  if (helper.startsWith('__VNGCLOUD_') || capability.startsWith('__VNGCLOUD_')) {
    throw new Error('run the private browser script printed by make browser-creds');
  }
  const fetchJSON = async (path, token) => {
    try {
      const resp = await page.request.get(helper + path, {
        headers: { Authorization: 'Bearer ' + token },
        maxRedirects: 0,
      });
      if (resp.ok()) {
        return await resp.json();
      }
    } catch {
    }
    throw new Error('browser credential helper unavailable; restart make browser-creds');
  };
  const fields = [
    page.getByRole('textbox', { name: 'Root email' }),
    page.getByRole('textbox', { name: 'Username' }),
    page.getByRole('textbox', { name: 'Password' }),
  ];
  const urlParts = () => {
    const m = page.url().match(/^(https?:\/\/[^/?#]+)([^?#]*)/);
    return { origin: m ? m[1] : '', pathname: m ? m[2] : '' };
  };
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

  const creds = await fetchJSON('/creds', capability);
  const next = creds.next;
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
    const totp = await fetchJSON('/totp', next);
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
