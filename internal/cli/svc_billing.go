package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/billing"
)

// billingOps is billing's operation table. Kinds follow the CLI table in the
// billing design: delete-budget and delete-budget-threshold are Write and
// Destructive; every other create and update is Write; every Get and List is
// Read.
var billingOps = []Op[billing.Client]{
	Read[billing.Client, billing.ListBudgetsInput, billing.ListBudgetsOutput](
		kebab("ListBudgets"), (*billing.Client).ListBudgets),
	Read[billing.Client, billing.GetBudgetInput, billing.GetBudgetOutput](
		kebab("GetBudget"), (*billing.Client).GetBudget),
	Write[billing.Client, billing.CreateBudgetInput, billing.CreateBudgetOutput](
		kebab("CreateBudget"), (*billing.Client).CreateBudget),
	Write[billing.Client, billing.UpdateBudgetInput, billing.UpdateBudgetOutput](
		kebab("UpdateBudget"), (*billing.Client).UpdateBudget),
	Write[billing.Client, billing.DeleteBudgetInput, billing.DeleteBudgetOutput](
		kebab("DeleteBudget"), (*billing.Client).DeleteBudget, Destructive()),
	Read[billing.Client, billing.GetCurrentPeriodCostInput, billing.GetCurrentPeriodCostOutput](
		kebab("GetCurrentPeriodCost"), (*billing.Client).GetCurrentPeriodCost),
	Read[billing.Client, billing.ListBudgetThresholdsInput, billing.ListBudgetThresholdsOutput](
		kebab("ListBudgetThresholds"), (*billing.Client).ListBudgetThresholds),
	Write[billing.Client, billing.CreateBudgetThresholdInput, billing.CreateBudgetThresholdOutput](
		kebab("CreateBudgetThreshold"), (*billing.Client).CreateBudgetThreshold),
	Write[billing.Client, billing.UpdateBudgetThresholdInput, billing.UpdateBudgetThresholdOutput](
		kebab("UpdateBudgetThreshold"), (*billing.Client).UpdateBudgetThreshold),
	Write[billing.Client, billing.DeleteBudgetThresholdInput, billing.DeleteBudgetThresholdOutput](
		kebab("DeleteBudgetThreshold"), (*billing.Client).DeleteBudgetThreshold, Destructive()),
	Read[billing.Client, billing.ListBudgetAlertsInput, billing.ListBudgetAlertsOutput](
		kebab("ListBudgetAlerts"), (*billing.Client).ListBudgetAlerts),
	Read[billing.Client, billing.GetCostOverviewInput, billing.GetCostOverviewOutput](
		kebab("GetCostOverview"), (*billing.Client).GetCostOverview),
	Read[billing.Client, billing.ListCostResourcesInput, billing.ListCostResourcesOutput](
		kebab("ListCostResources"), (*billing.Client).ListCostResources),
	Read[billing.Client, billing.GetBalancesInput, billing.GetBalancesOutput](
		kebab("GetBalances"), (*billing.Client).GetBalances),
}

func newBillingCmd(e *env) *cobra.Command {
	return Service(e, "billing", billing.New, billingOps...)
}
