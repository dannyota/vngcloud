package network

import (
	"context"
	"time"
)

const endpointPoll = 5 * time.Second

func (o *endpointOp) expired(ctx context.Context, deadline time.Time) error {
	if ctx.Err() != nil || !o.c.now().Before(deadline) {
		return endpointUnsettled(o.op, natDeadlineCause(ctx))
	}
	return nil
}

func (o *endpointOp) pause(ctx context.Context, deadline time.Time) error {
	remaining := deadline.Sub(o.c.now())
	if remaining <= 0 {
		return endpointUnsettled(o.op, natDeadlineCause(ctx))
	}
	if err := o.c.sleep(ctx, min(endpointPoll, remaining)); err != nil {
		return endpointUnsettled(o.op, err)
	}
	return nil
}

func (o *endpointOp) waitCreate(ctx context.Context, s *endpointSpec, baseline map[string]bool, out *CreateEndpointOutput, deadline time.Time) error {
	for {
		if err := o.expired(ctx, deadline); err != nil {
			return err
		}
		rows, err := o.inventory(ctx)
		if err != nil {
			return endpointUnsettled(o.op, err)
		}
		if err := o.expired(ctx, deadline); err != nil {
			return err
		}
		for _, row := range rows {
			if row.UUID != out.EndpointID {
				continue
			}
			if baseline[row.UUID] || row.EndpointName != s.info.EndpointName || row.VPCID != s.info.VPCUUID || row.Subnet.UUID != s.info.SubnetUUID ||
				row.Service.UUID != s.info.ServiceUUID || row.Service.Name != s.offer.serviceName || row.PackageID.UUID != s.info.PackageUUID || row.Project.VServerProjectID != s.info.ProjectUUID ||
				row.ZoneUUID != s.offer.zone || row.EndpointType != "Gateway" || row.EnableDNSName {
				return endpointUnsettled(o.op)
			}
			out.Status = row.Status
			switch row.Status {
			case "ACTIVE":
				return nil
			case "PROVISIONING":
			default:
				return endpointUnsettled(o.op)
			}
		}
		if err := o.pause(ctx, deadline); err != nil {
			return err
		}
	}
}

func (o *endpointOp) waitDelete(ctx context.Context, id string, deadline time.Time) error {
	for {
		if err := o.expired(ctx, deadline); err != nil {
			return err
		}
		rows, err := o.deleteInventory(ctx)
		if err != nil {
			return endpointUnsettled(o.op, err)
		}
		if err := o.expired(ctx, deadline); err != nil {
			return err
		}
		present := false
		for _, row := range rows {
			if row.UUID == id {
				present = true
				if row.Status != "ACTIVE" && row.Status != "DELETING" {
					return endpointUnsettled(o.op)
				}
			}
		}
		if !present {
			return nil
		}
		if err := o.pause(ctx, deadline); err != nil {
			return err
		}
	}
}
