package main

import (
	"context"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/pricing"
)

// showBilling records the account-level billing calls: budgets, the current
// billing period's cost, balances, and history. Billing ignores the configured
// region, so main calls this once per config, not once per region.
func showBilling(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	client := billing.New(cfg)

	resources, err := client.ListResources(ctx, nil)
	recordAccountOne(outputs, "billing/resources", "billing resources", resources, err)

	budgets, err := client.ListBudgets(ctx, &billing.ListBudgetsInput{})
	recordAccount(outputs, "billing/budget", "billing budgets", budgetItems(budgets), err)

	cost, err := client.GetCurrentPeriodCost(ctx, &billing.GetCurrentPeriodCostInput{})
	recordAccountOne(outputs, "billing/current_period_cost", "billing current period cost", cost, err)

	balances, err := client.GetBalances(ctx, &billing.GetBalancesInput{})
	recordAccountOne(outputs, "billing/balance", "billing balances", balances, err)

	today := time.Now().In(time.FixedZone("UTC+07:00", 7*60*60))
	start, end := today.AddDate(0, 0, -29).Format("2006-01-02"), today.Format("2006-01-02")
	transactions, err := client.ListTransactions(ctx, &billing.ListTransactionsInput{StartDate: start, EndDate: end, Page: 1, Size: 10})
	recordAccountOne(outputs, "billing/transaction", "billing transactions", transactions, err)
	payments, err := client.ListPayments(ctx, &billing.ListPaymentsInput{StartDate: start, EndDate: end, Page: 1, Size: 10})
	recordAccountOne(outputs, "billing/payment", "billing payments", payments, err)
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
