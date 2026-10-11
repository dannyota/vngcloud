# Service-account fixture provenance

These fixtures are synthetic raw bodies built from the response shapes that
live checks recorded on the owner's test account. The
[service-account design](../../docs/design/service-account-auth.md) and its
[evidence](../../docs/design/service-account-auth-api.md) record what the
checks showed. No live token, secret, or raw create, reset, or token body was
saved at any stage, so every value below is synthetic.

|Fixture|Request|Status|Observed shape|Synthetic values|
|-|-|-|-|-|
|`service_account_token.json`|POST `https://iamapis.vngcloud.vn/accounts-api/v2/auth/token` with Basic client credentials and `{"grant_type":"client_credentials"}`|200|Object with `access_token` (string), `token_type` (string), and `expires_in` (number, 1800 by default); no refresh keys; `application/json`|`access_token` is `<secret>`; `token_type` is `Bearer`, a documented example, because the check recorded only its type|
|`service_account_authentication_failed.json`|Same request with a reset or deleted secret|401|Object with `errors`, an array of objects whose `code` was `AUTHENTICATION_FAILED`|`message` is `<message>`; the check recorded no message text|
|`service_account_forbidden.json`|GET `{V}/v2/{projectId}/secgroups/{id}` for a group outside the service account's policy|403|Top-level array of objects with string `code` and `message`|Both values are synthetic: the check recorded only their masked shapes|

Tests that build adversarial variants from these files label them synthetic.
