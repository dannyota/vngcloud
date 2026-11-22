package pricing

import "math"

func (d *estimateRun) finish() (*EstimateOutput, error) {
	sum := 0.0
	quoted, survivors := 0, 0
	overflow := false
	for _, item := range d.output.Items {
		switch item.Price.Status {
		case "quoted":
			survivors++
			quoted++
			sum += *item.MonthlyAmount
			if math.IsInf(sum, 0) || math.IsNaN(sum) {
				overflow = true
			}
		case "unknown", "inapplicable":
			survivors++
		}
	}
	if overflow {
		err := &EstimateArithmeticError{Stage: "subtotal"}
		if !d.input.AllowPartial {
			return nil, err
		}
		d.record("", err)
	} else if quoted > 0 {
		d.output.Total.KnownMonthlySubtotal = &sum
	}
	d.output.Total.RequestedComplete = d.output.Metadata.Complete && quoted == len(d.output.Items)
	if d.output.Total.RequestedComplete {
		d.output.Total.RequestedMonthlyTotal = &sum
	}
	for _, item := range d.output.Items {
		if item.Price.Status != "failed" {
			continue
		}
		recorded := false
		for _, failure := range d.output.Metadata.Failures {
			if failure.ItemKey == item.Key {
				recorded = true
				break
			}
		}
		if !recorded {
			d.output.Coverage = append(d.output.Coverage, EstimateCoverage{Topic: "processing", Scope: item.Key, Status: "failed", Reason: "not-attempted"})
		}
	}
	d.output.Metadata.Requests = d.session.Requests()
	d.output.Metadata.QuotedAt = d.session.CompletedAt()
	if len(d.errors) > 0 {
		if survivors == 0 {
			return nil, d.errors[0]
		}
		return d.output, &DiscoveryError{Errors: d.errors}
	}
	return d.output, nil
}

func estimateNotes() []PricingNote {
	notes := []PricingNote{}
	for _, note := range DiscoveryNotes() {
		switch note.Topic {
		case "account-scope", "terms", "egress", "bandwidth":
			notes = append(notes, note)
		}
	}
	return append(notes,
		PricingNote{Topic: "estimate", Scope: "setup", Text: "monthly, VAT as returned, account-scoped estimate; not a commitment", Evidence: "projection"},
		PricingNote{Topic: "quantity", Scope: "setup", Text: "Quantities project independent unit quotes without bulk discounts or shared allowances. Quotes do not establish deployment feasibility or capacity.", Evidence: "projection"},
	)
}

func (d *estimateRun) addCoverage() {
	for _, topic := range []string{"bandwidth-packages", "software-licenses", "nat", "load-balancers", "backups", "snapshots", "payg", "negotiated-discounts-credits"} {
		d.output.Coverage = append(d.output.Coverage, EstimateCoverage{Topic: topic, Scope: "setup", Status: "excluded", Reason: "outside-estimate-scope"})
	}
	traffic := false
	for _, item := range d.output.Items {
		switch item.Kind {
		case "server":
			traffic = true
			d.output.Coverage = append(d.output.Coverage, EstimateCoverage{Topic: "vserver-traffic", Scope: item.Key, Status: "unknown", Reason: "usage-not-priced"})
			if len(d.input.PublicIPs) == 0 {
				d.output.Coverage = append(d.output.Coverage, EstimateCoverage{Topic: "public-ip", Scope: item.Key, Status: "excluded", Reason: "no IP product specified; any required IP cost is excluded"})
			}
		case "object-storage":
			d.output.Coverage = append(d.output.Coverage, EstimateCoverage{Topic: "object-egress-requests", Scope: item.Key, Status: "unknown", Reason: "usage-not-priced"})
		}
	}
	if !traffic {
		d.output.Coverage = append(d.output.Coverage, EstimateCoverage{Topic: "vserver-traffic", Scope: "setup", Status: "excluded", Reason: "outside-estimate-scope"})
	}
	if len(d.input.Storage) == 0 {
		d.output.Coverage = append(d.output.Coverage, EstimateCoverage{Topic: "object-egress-requests", Scope: "setup", Status: "excluded", Reason: "outside-estimate-scope"})
	}
}
