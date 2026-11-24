# Backup Center GetPolicy plan

**Goal:** Add the verified `hcm-3` IAM policy detail read to the SDK and CLI without widening the policy projection.

**Contract:** [Policy detail](../design/backup-policy-detail.md), [existing Backup Center rules](../design/backup-services.md), and [API evidence](../design/backup-services-api.md#observed-policy-detail).

## Before code

- [x] Confirm the HCM host, bearer access, no-query detail route, HTTP 200, exact requested ID, and direct response shape by a scoped live read.
- [x] Verify the provider count decodes as Go `int`, record a controlled missing-ID response, and prepare a raw-derived sanitized fixture. Keep data values and account information out of the repository.
- [x] Reconcile the parent scope and detail contract; keep Hanoi, other routes, cadence children, account identity, and mutations outside this slice.

## Implementation

- [x] Add failing SDK tests in `backup/` using `testdata/backup/get-policy.json`. Require exact method, host, path, empty query, matching identity, strict required keys, and no authentication for bad input or unsupported region.
- [x] Define `GetPolicyInput{PolicyID string}` and `GetPolicyOutput{Policy Policy}` and implement one read. Keep the existing `Policy` projection, status and error safety, and all list behavior. Test absent, null, empty and explicit daily values plus malformed, oversized and hostile responses.
- [x] Register `backup get-policy --policy-id` as read-only in `internal/cli/svc_backup.go`. Test existing output modes, input precedence, no output on error, and no leaking omitted response data. Update backup wiki/help and regenerate CLI docs.
- [x] Verify the SDK read against the same approved HCM policy with output sanitized in-process. Check identity and the populated daily configuration.
- [ ] Run the full pinned checks after integration. Obtain independent review and exact-head CI before landing and tagging.
