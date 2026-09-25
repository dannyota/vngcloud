package main

import (
	"context"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/pricing"
)

// showBilling records the account-level billing calls: budgets, the current
// billing period's cost, and balances. Billing ignores the configured
// region, so main calls this once per config, not once per region.
func showBilling(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	client := billing.New(cfg)

	budgets, err := client.ListBudgets(ctx, &billing.ListBudgetsInput{})
	recordAccount(outputs, "billing/budget", "billing budgets", budgetItems(budgets), err)

	cost, err := client.GetCurrentPeriodCost(ctx, &billing.GetCurrentPeriodCostInput{})
	recordAccountOne(outputs, "billing/current_period_cost", "billing current period cost", cost, err)

	balances, err := client.GetBalances(ctx, &billing.GetBalancesInput{})
	recordAccountOne(outputs, "billing/balance", "billing balances", balances, err)
}

func budgetItems(result *billing.ListBudgetsOutput) []billing.Budget {
	if result == nil {
		return nil
	}
	return result.Items
}

// showPricing records a snapshot price quote for region. Unlike the rest of
// billing, GetQuote is scoped to the regional billing gateway.
func showPricing(ctx context.Context, cfg vngcloud.Config, region string, outputs *sdkOutputStore) {
	client := pricing.New(cfg)

	quote, err := client.GetQuote(ctx, &pricing.GetQuoteInput{ResourceType: pricing.ResourceSnapshot})
	recordPricing(outputs, region, "pricing/quote", "pricing snapshot quote", quote, err)
}
