package cli

import (
	"context"
	"errors"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/cdn"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/containerregistry"
	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/iam"
	"danny.vn/vngcloud/loadbalancer"
	"danny.vn/vngcloud/monitor"
	"danny.vn/vngcloud/network"
	"danny.vn/vngcloud/pricing"
	"danny.vn/vngcloud/storage"
	"danny.vn/vngcloud/tagging"
	"danny.vn/vngcloud/volume"
)

// exitCode maps err to the CLI's process exit code, checked in this fixed
// order: an unconfirmed object copy or deletion always exits 1 before any wrapped
// auth, not-found, or context error. A canceled or expired context exits 1,
// even for a *LoginError or an *APIError that wraps it, because the command
// was interrupted rather than genuinely rejected. Missing or refused
// credentials and every login failure exit 3. A not-found result exits 4.
// Every remaining usage or config problem exits 2. Anything else exits 1.
func exitCode(err error) int {
	if errors.Is(err, compute.ErrOpenLogin) || errors.Is(err, monitor.ErrQuotaStatusIncomplete) {
		return 1
	}
	var resetFileErr resetPasswordFileError
	if errors.As(err, &resetFileErr) || errors.Is(err, iam.ErrUserWriteUncertain) {
		return 1
	}
	if errors.Is(err, iam.ErrUserCreateUncertain) || errors.Is(err, iam.ErrCreateUnconfirmed) || errors.Is(err, iam.ErrOriginNotIAM) {
		return 1
	}
	if errors.Is(err, storage.ErrObjectCopyUnconfirmed) || errors.Is(err, storage.ErrObjectDeleteUnconfirmed) {
		return 1
	}
	if errors.Is(err, storage.ErrObjectWriteUnconfirmed) || errors.Is(err, storage.ErrObjectExists) {
		return 1
	}
	if errors.Is(err, pricing.ErrDiscoveryIncomplete) {
		return 1
	}
	if errors.Is(err, storage.ErrBucketEncryptionIncomplete) {
		return 1
	}
	if err == nil {
		return 0
	}
	// Checked before the canceled-context rule below: a Ctrl-C during the
	// toggle PUT or a confirm read still reports the same exit code (1) as
	// every other unconfirmed toggle, per monitor's design, rather than
	// happening to match the canceled-context rule by coincidence. dns.ErrZoneBusy,
	// dns.ErrFailed, dns.ErrNotSettled, network.ErrFailed, network.ErrNotSettled,
	// network.ErrUnexpectedStatus, compute.ErrFailed, compute.ErrNotSettled,
	// compute.ErrUnexpectedStatus, containerregistry.ErrNotSettled,
	// containerregistry.ErrUserNotFound, iam.ErrNotSettled,
	// tagging.ErrNotSettled, CDN's purge, settle, and status sentinels, monitor.ErrOTPRejected, volume.ErrFailed,
	// volume.ErrNotSettled, volume.ErrVolumeInUse,
	// volume.ErrUnexpectedStatus, loadbalancer.ErrFailed, and
	// loadbalancer.ErrNotSettled join the same early return for the same
	// reason: per the vDNS, network, vServer, vCR writes, and iam designs, a
	// not-settled write, and a create-user whose own create already
	// succeeded, must exit the same way even after a canceled context,
	// because the write already landed, and the others join it for
	// consistency. tagging's own confirm read can also fail with a wrapped
	// *core.APIError of any status, including 404, so this check must win
	// over the later NotFound check too, the same reason it wins over
	// classify's generic *APIError branch.
	var attachFailed attachFailedError
	if errors.As(err, &attachFailed) {
		return 1
	}
	if errors.Is(err, cdn.ErrPurgeCooldown) || errors.Is(err, cdn.ErrStatusUnconfirmed) || errors.Is(err, cdn.ErrUnexpectedStatus) || errors.Is(err, cdn.ErrNotSettled) || errors.Is(err, cdn.ErrBusy) ||
		errors.Is(err, monitor.ErrStatusUnconfirmed) || errors.Is(err, monitor.ErrUnexpectedStatus) ||
		errors.Is(err, network.ErrUnexpectedStatus) || errors.Is(err, compute.ErrUnexpectedStatus) || errors.Is(err, volume.ErrUnexpectedStatus) ||
		errors.Is(err, dns.ErrZoneBusy) || errors.Is(err, dns.ErrFailed) || errors.Is(err, dns.ErrNotSettled) ||
		errors.Is(err, network.ErrFailed) || errors.Is(err, network.ErrNotSettled) ||
		errors.Is(err, compute.ErrFailed) || errors.Is(err, compute.ErrNotSettled) || errors.Is(err, containerregistry.ErrNotSettled) ||
		errors.Is(err, containerregistry.ErrUserNotFound) || errors.Is(err, iam.ErrNotSettled) ||
		errors.Is(err, tagging.ErrNotSettled) || errors.Is(err, monitor.ErrOTPRejected) ||
		errors.Is(err, volume.ErrFailed) || errors.Is(err, volume.ErrNotSettled) || errors.Is(err, volume.ErrVolumeInUse) ||
		errors.Is(err, loadbalancer.ErrFailed) || errors.Is(err, loadbalancer.ErrNotSettled) ||
		errors.Is(err, storage.ErrNotSettled) || errors.Is(err, storage.ErrFailed) ||
		errors.Is(err, storage.ErrPaymentRequired) || errors.Is(err, storage.ErrProjectNotEmpty) {
		return 1
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 1
	}
	if errors.Is(err, vngcloud.ErrNoCredentials) || errors.Is(err, vngcloud.ErrCredentialsFile) {
		return 3
	}
	var loginErr *vngcloud.LoginError
	if errors.As(err, &loginErr) {
		return 3
	}
	var apiErr *vngcloud.APIError
	if errors.Is(err, vngcloud.ErrAuth) || (errors.As(err, &apiErr) && apiErr.StatusCode == 401) {
		return 3
	}
	if vngcloud.IsNotFound(err) {
		return 4
	}
	// A runtime --query failure (queryFailedError) is deliberately not listed
	// here: it means the operation itself already succeeded, so it falls
	// through to the plain exit 1 below rather than the usage-error exit 2,
	// per the CLI design.
	var usageErr usageError
	var roErr readOnlyError
	switch {
	case errors.As(err, &usageErr),
		errors.As(err, &roErr),
		errors.Is(err, vngcloud.ErrInvalidInput),
		errors.Is(err, vngcloud.ErrInvalidConfig),
		errors.Is(err, vngcloud.ErrProjectAmbiguous):
		return 2
	}
	// vngcloud.ErrPriceAboveMax (and monitor.ErrPriceAboveMax, the same
	// value) and vngcloud.ErrUnpriced also exit 1 here, through this default: a paid write's price
	// guard returns it directly, before any request, never wrapped alongside
	// a canceled context the way dns.ErrNotSettled can be, so it needs no
	// earlier special-case check.
	return 1
}
