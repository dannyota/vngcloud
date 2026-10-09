package cli

// loadbalancerImportCertificateNote documents import-certificate's file
// flags and their rules, none of which the flag table can show on its own:
// --name and --type still take ordinary flags, but every field that can
// hold PEM or key text comes from a file instead.
const loadbalancerImportCertificateNote = "Certificate, CertificateChain, PrivateKey, and Passphrase all come " +
	"from a file: --certificate-file (required), --certificate-chain-file, --private-key-file, and " +
	"--passphrase-file; none of the four has a plain string flag, and --cli-input-json refuses PrivateKey and " +
	"Passphrase outright, inline or file://, though Certificate and CertificateChain may still be set that " +
	"way instead of by file. Each file is read whole, at most 64 KiB, and an empty file is refused; " +
	"--passphrase-file additionally drops one trailing newline. PrivateKey is required for Type TLS/SSL; for " +
	"any other Type, PrivateKey, Passphrase, and CertificateChain must all be empty. GreenNode keeps the key, " +
	"and the printed Certificate holds no key field. A failing import withholds the server's own error " +
	"message entirely, since it could otherwise quote the rejected key or passphrase back. Keep the private " +
	"key file readable only by its owner (chmod 600)."

// loadbalancerDeleteCertificateNote documents delete-certificate's
// pre-delete guard and why it needs --yes: the flag table shows only
// --certificate-id.
const loadbalancerDeleteCertificateNote = "Refuses, before any request, a certificate a listener still uses " +
	"(error code ResourceInUse), read first with get-certificate. A deleted certificate needs its key again " +
	"to re-import, and the key may no longer exist anywhere else, so this needs --yes."

// loadbalancerQuoteCreateLoadBalancerNote documents quote-create-load-balancer's
// own price guard exemptions and unit, and that the billing gateway prices
// only PackageID and ZoneID: the flag table shows every
// CreateLoadBalancerInput field the same way create-load-balancer itself
// will, with no hint that this command never orders anything or that most
// of those fields do nothing here.
const loadbalancerQuoteCreateLoadBalancerNote = "Never orders anything: prices the load balancer " +
	"CreateLoadBalancerInput describes without sending a create. OptimumPrice and every other price are VND " +
	"a month, one prepaid period. Ignores MaxPrice and NoWait even when an inline --cli-input-json value sets " +
	"them: both govern only an actual create. The billing gateway also ignores every key it does not price, " +
	"such as Name, Scheme, SubnetID, or Type: changing them does not change the quoted price."

// loadbalancerQuoteResizeLoadBalancerNote documents quote-resize-load-balancer's
// own price guard exemptions, unit, and its not-found status, which differs
// from every other load-balancer command's: the flag table shows only
// --load-balancer-id and --package-id, with no hint of any of this.
const loadbalancerQuoteResizeLoadBalancerNote = "Never orders anything: prices the package change " +
	"ResizeLoadBalancerInput describes without sending a resize. OptimumPrice and every other price are VND " +
	"a month. Ignores MaxPrice and NoWait even when an inline --cli-input-json value sets them: both govern " +
	"only an actual resize. --load-balancer-id naming a load balancer that does not exist exits 1 with the " +
	"server's own status 400 message, not NotFound: unlike every other load-balancer command, the server " +
	"checks this request's shape before it checks the ID."

// loadbalancerCreateLoadBalancerNote documents create-load-balancer's price
// guard default, its MaxPrice guard, the unretried order, the post-order
// wait bound, and the Scheme guard: the flag table shows Scheme as a plain,
// unconditional string, with no hint that only one value skips --yes.
const loadbalancerCreateLoadBalancerNote = "Run quote-create-load-balancer first, and set a billing budget " +
	"with an alert before any paid create: this command orders nothing above --max-price, default 0, so a " +
	"bare create-load-balancer refuses every package. A quote of 0 is refused, and --max-price must be at " +
	"least the quote. --max-price NaN, " +
	"Inf, or negative exits 2 (InvalidUsage) before any request. The quote and the order build from the same " +
	"fields, so the order always prices what was just quoted. The order itself is never retried after a " +
	"failure that may have already reached the server; list load balancers by name " +
	"(list-load-balancers --name) and match it exactly before ordering again rather than repeating this " +
	"command. Without --no-wait, waits up to 20 minutes for the new load balancer to reach CREATED; ERROR " +
	"during that wait is WriteFailed, and a timeout is NotSettled, either way with the last-read load " +
	"balancer printed alongside the error, and the write must not be repeated. Every Scheme except Internal, " +
	"trimmed of surrounding space and matched case-insensitively, needs --yes: only a load balancer created " +
	"with Scheme Internal is confirmed to stay off the public internet."

// loadbalancerDeleteLoadBalancerNote documents delete-load-balancer's
// read-first behavior, its post-delete wait, and that what happens to its
// listeners and pools is unverified live: the flag table shows only
// --load-balancer-id, with no hint of any of this.
const loadbalancerDeleteLoadBalancerNote = "Reads the load balancer first: an unknown --load-balancer-id is " +
	"NotFound, and one already DELETING is waited on rather than sent a second DELETE. Deleting a load " +
	"balancer loses its address and its prepaid time for good; what happens to its listeners and pools, if " +
	"any exist, is unverified live. Without --no-wait, waits up to 15 minutes for the load balancer to 404; " +
	"ERROR during that wait is WriteFailed, and a timeout is NotSettled, but a rerun is always safe: this " +
	"command reads first."

// loadbalancerResizeLoadBalancerNote documents resize-load-balancer's
// no-op-on-same-package shortcut, its price guard, its immediate busy
// refusal, and its post-resize wait bound: the flag table shows only
// --load-balancer-id, --package-id, --max-price, and --no-wait, with no
// hint of any of this.
const loadbalancerResizeLoadBalancerNote = "Reads the load balancer first: --package-id equal to its current " +
	"package makes this a no-op, Changed false, quoting and sending nothing. Otherwise orders nothing above " +
	"--max-price, default 0; --max-price NaN, Inf, or negative exits 2 (InvalidUsage) before any request. A " +
	"downsize's quote may legitimately price below zero as a refund, which never exceeds --max-price. The " +
	"resize is sent once and never resent, whatever the failure: a busy load balancer refuses the resize " +
	"itself with ResourceBusy at once, rather than waiting and resending the way a free write's busy resend " +
	"does. After any failure, read the load balancer with get-load-balancer and compare its package to the " +
	"one just requested before running this command again, rather than rerunning it blind: a resize failure " +
	"does not say whether the order reached the server. Without --no-wait, waits up to 45 minutes for the " +
	"load balancer to reach CREATED with the new package; ERROR " +
	"during that wait is WriteFailed, and a timeout is NotSettled, either way with the last-read load " +
	"balancer printed alongside the error, and the write must not be repeated."

// loadbalancerCreatePoolNote documents create-pool's health monitor
// defaults and its HTTP-fields guard: the flag table shows every health
// check field as a plain, independent flag, with no hint that five of them
// depend on --health-check-protocol.
const loadbalancerCreatePoolNote = "Empty --algorithm sends ROUND_ROBIN; 0 --healthy-threshold, " +
	"--unhealthy-threshold, --health-check-interval, or --health-check-timeout sends the server's own default " +
	"(3, 3, 30, and 5). --stickiness and --tls-encryption are sent only when given, since a Layer 4 pool has " +
	"no use for either. --health-check-path, --health-check-method, --health-check-http-version, " +
	"--health-check-domain-name, and --health-check-success-code are only valid when --health-check-protocol " +
	"is HTTP or HTTPS; giving one with any other check protocol exits 2 (InvalidUsage) before any request. No " +
	"domain name is invented for HTTP/1.1: an HTTP check with none reaches the server empty, which then " +
	"refuses it."

// loadbalancerUpdatePoolNote documents update-pool's read-merge and its
// shared HTTP-fields guard: the flag table shows every field as
// independently optional, with no hint that at least one is required or
// that the HTTP fields depend on the pool's own, unchangeable check
// protocol.
const loadbalancerUpdatePoolNote = "At least one field must be set, checked before any request " +
	"(InvalidUsage). Reads the pool and its health monitor, applies every set field, and sends the full body " +
	"with the read values for the rest, so a call that sets only --algorithm still resends the health monitor " +
	"exactly as read. The health check protocol cannot change after create, so there is no flag for it here; " +
	"the HTTP fields are refused when the pool's own check protocol is not HTTP or HTTPS, the same guard " +
	"create-pool runs."

// loadbalancerDeletePoolNote documents delete-pool's pre-delete guard: the
// flag table shows only --pool-id, with no hint of the read this command
// makes before its own DELETE.
const loadbalancerDeletePoolNote = "Refuses, before any request, a pool a listener still names as its " +
	"default pool (error code ResourceInUse), found by listing the load balancer's listeners; a policy that " +
	"redirects to the pool is left to the server's own refusal, which this command maps the same way."

// loadbalancerPoolMemberNote is shared by add-pool-member and
// update-pool-member: both are read-merge writes over the pool's whole
// member list, which the flag table cannot show at all.
const loadbalancerPoolMemberNote = "The members write replaces the whole list: this command reads every " +
	"member first and sends back what it read plus this one change, never a caller-supplied whole list. " +
	"--address must be IPv4; --port and --monitor-port are 1 to 65535, with --monitor-port 0 meaning unset. " +
	"--weight 0 sends 1. Without --no-wait, waits for the pool to settle, then confirms a fresh read names " +
	"exactly the members just sent; a mismatch, a timeout, or the pool reaching ERROR is NotSettled or " +
	"WriteFailed, and Changed still reports whether the member list actually changed."

// loadbalancerAddPoolMemberNote extends loadbalancerPoolMemberNote with
// add-pool-member's own no-op and conflict rules.
const loadbalancerAddPoolMemberNote = loadbalancerPoolMemberNote + " A member already present at " +
	"--address and --port with the same fields makes this a no-op: Changed false, nothing sent. One present " +
	"with any different field exits 2 (InvalidUsage) naming update-pool-member instead."

// loadbalancerUpdatePoolMemberNote extends loadbalancerPoolMemberNote with
// update-pool-member's own required-field and not-found rules.
const loadbalancerUpdatePoolMemberNote = loadbalancerPoolMemberNote + " At least one of --name, --weight, " +
	"--monitor-port, or --backup must be set, checked before any request (InvalidUsage). No member at " +
	"--address and --port is NotFound, nothing sent; a change that leaves every field equal to what was read " +
	"is Changed false, nothing sent."

// loadbalancerRemovePoolMemberNote extends loadbalancerPoolMemberNote with
// remove-pool-member's own not-found rule and its --yes requirement, which
// the flag table cannot show at all since it is not Destructive.
const loadbalancerRemovePoolMemberNote = loadbalancerPoolMemberNote + " No member at --address and --port " +
	"is NotFound, nothing sent. Needs --yes on every call: removing a member stops it taking traffic " +
	"immediately, though add-pool-member can restore it."

// loadbalancerCreateListenerNote documents create-listener's own
// --allowed-cidrs flag, its private-range guard, its HTTPS certificate rule,
// and its cleartext warning: the flag table shows every certificate field
// as an independent, unconditional flag, with no hint that any one of them
// set with the wrong Protocol is refused.
const loadbalancerCreateListenerNote = "--allowed-cidrs is a comma-separated list of IPv4 CIDR prefixes with " +
	"no host bits set, such as 10.0.0.0/24,203.0.113.0/28; it is required, with no default, unlike VNG " +
	"Cloud's own SDK, which sends 0.0.0.0/0. Any entry outside every private range (10.0.0.0/8, " +
	"172.16.0.0/12, 192.168.0.0/16, 100.64.0.0/10) needs --yes: it may open the ports this listener names to " +
	"outside the account's own network, more so on a load balancer created with a Scheme other than Internal. " +
	"--protocol HTTPS requires --default-certificate-id; any other protocol refuses --default-certificate-id, " +
	"--client-certificate-id, and a --cli-input-json CertificateIDs alike, refusing the command the moment " +
	"any single one of them is set. CertificateIDs comes only through --cli-input-json, per the design's " +
	"nested fields, as does --cli-input-json's InsertHeaders. A 0 --timeout-client, --timeout-member, or " +
	"--timeout-connection sends the server's own default (50, 50, and 5 seconds). An HTTP listener on a " +
	"load balancer reachable from outside the " +
	"account's own network serves cleartext: nothing encrypts traffic between the client and the load " +
	"balancer."

// loadbalancerUpdateListenerNote documents update-listener's read-merge,
// its own --allowed-cidrs flag and private-range guard, and its
// empty-AllowedCIDRs refusal: the flag table shows every field as
// independently optional, with no hint of any of this.
const loadbalancerUpdateListenerNote = "At least one field must be set, checked before any request " +
	"(InvalidUsage). Reads the listener, applies every set field, and sends the full body with the read " +
	"values for the rest. --allowed-cidrs takes the same comma-separated value create-listener's does; " +
	"leaving it unset keeps the listener's own value, and a set one needs --yes for an entry outside every " +
	"private range (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 100.64.0.0/10) the same way. An explicitly " +
	"empty --allowed-cidrs reaches the SDK as a non-nil, empty list, which it refuses (InvalidUsage) rather " +
	"than send, since an empty list would open or close the listener to everyone depending on the server's " +
	"own interpretation. The merged certificate fields are checked against the listener's own, unchangeable " +
	"Protocol exactly as create-listener checks them; CertificateIDs comes only through --cli-input-json."

// loadbalancerDeleteListenerNote documents delete-listener's pre-write
// wait, which the flag table cannot show at all: it shows only
// --listener-id.
const loadbalancerDeleteListenerNote = "Waits, within the pre-write bound, until the load balancer and the " +
	"listener are both not busy (error code ResourceBusy, nothing sent, past that bound), then sends the " +
	"DELETE."

// loadbalancerCreatePolicyNote documents create-policy's Rules field and
// its redirect guard: the flag table shows Rules as
// "via --cli-input-json only" and every redirect field as an independent,
// unconditional flag.
const loadbalancerCreatePolicyNote = "Rules comes only through --cli-input-json, a list of objects each " +
	"with Type, CompareType, and Value all required, for example " +
	`'{"Rules":[{"Type":"PATH","CompareType":"STARTS_WITH","Value":"/api"}]}'` + ". --action REDIRECT_TO_POOL " +
	"requires --redirect-pool-id and refuses --redirect-url; REDIRECT_TO_URL requires --redirect-url and " +
	"refuses --redirect-pool-id. Another --action value reaches the server as given."

// loadbalancerUpdatePolicyNote documents update-policy's read-merge and its
// own Rules-replace rule: the flag table shows every field as
// independently optional, with no hint that at least one is required or
// that a set Rules replaces the whole list.
const loadbalancerUpdatePolicyNote = "At least one field must be set, checked before any request " +
	"(InvalidUsage). Reads the policy, applies every set field, and sends the full body with the read values " +
	"for the rest. A --cli-input-json Rules replaces the whole rule list; leaving it unset resends the rules " +
	"read, refusing the write instead if one of them reads back missing Type, CompareType, or Value. The " +
	"merged --action and redirect fields are checked exactly as create-policy checks them."

// loadbalancerDeletePolicyNote documents delete-policy's pre-write wait,
// which the flag table cannot show at all: it shows only --policy-id.
const loadbalancerDeletePolicyNote = "Waits, within the pre-write bound, until the load balancer and the " +
	"policy are both not busy (error code ResourceBusy, nothing sent, past that bound), then sends the DELETE."
