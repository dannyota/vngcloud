//go:build livewrite

package livetest_test

import (
	"context"
	"errors"
	"time"

	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/network"
)

type natReconcileState struct {
	client                     *network.Client
	money                      *billing.Client
	report                     map[string]any
	zone, vpc, nat             string
	name, project, pkg         string
	baseline                   []network.NATInstance
	before                     float64
	after                      *float64
	cashErr                    *error
	cashRead, natGone, vpcGone *bool
}

func (s natReconcileState) run(ctx context.Context) error {
	if !*s.cashRead {
		cashCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		*s.after, *s.cashErr = storageProjectCash(cashCtx, s.money)
		cancel()
		*s.cashRead = *s.cashErr == nil
		recordNATPostOrderCash(s.report, s.before, *s.after, *s.cashErr)
	}
	s.report["cleanupIdentityConfirmed"] = false
	inventory, err := liveNATInventory(ctx, s.client, s.zone)
	if err != nil {
		return errors.New("NAT cleanup identity could not be verified")
	}
	liveNATRecordCleanupCandidates(s.report, inventory, s.name)
	if _, ok := liveNATDeleteIdentity(inventory, s.baseline, s.name, s.project, s.zone, s.vpc, s.pkg, s.nat); !ok {
		return errors.New("NAT cleanup identity could not be verified")
	}
	s.report["cleanupIdentityConfirmed"] = true
	if _, err := s.client.DeleteNATInstance(ctx, &network.DeleteNATInstanceInput{ZoneID: s.zone, VPCID: s.vpc, NATID: s.nat}); err != nil {
		return errors.New("NAT delete failed; inspect leftovers before another action")
	}
	after, err := liveNATInventory(ctx, s.client, s.zone)
	if err != nil {
		return errors.New("NAT absence confirmation failed")
	}
	for _, row := range after {
		if row.UUID == s.nat {
			return errors.New("NAT remains after delete")
		}
	}
	*s.natGone = true
	routes, err := liveNATRoutes(ctx, s.client, s.vpc)
	if err != nil || len(routes) != 0 {
		return errors.New("NAT route removal was not confirmed")
	}
	s.report["afterDeleteRoutes"] = routes
	if !liveNATBillingAbsent(ctx, s.money, s.nat) {
		return errors.New("NAT billing row remains after delete")
	}
	s.report["billingAbsent"] = true
	if !liveNATDeleteVPC(ctx, s.client, s.vpc) {
		return errors.New("VPC delete did not settle within 10 minutes")
	}
	*s.vpcGone = true
	if !*s.cashRead {
		return errors.New("NAT debit unavailable; refund cannot be reconciled")
	}
	finalCash, refundErr := liveNATRefund(ctx, s.money, s.before, *s.after)
	s.report["finalCash"] = finalCash
	s.report["refund"] = finalCash - *s.after
	s.report["netCost"] = s.before - finalCash
	if refundErr != nil {
		return errors.New("NAT refund remains unresolved; reconcile the private report and transactions")
	}
	return nil
}
