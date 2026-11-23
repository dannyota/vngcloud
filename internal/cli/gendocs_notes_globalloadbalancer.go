package cli

const globalLoadBalancerChildBilling = "No quote is available, and the request carries no price or payment field. GreenNode bills child usage under the account's terms; the SDK neither selects nor verifies the payment source. Deleting does not promise a refund."

func init() {
	docExampleOverride["globalloadbalancer create-load-balancer"] = "vngcloud globalloadbalancer create-load-balancer --name <name> --package-id <package-id> --yes"
	docExampleOverride["globalloadbalancer create-pool"] = "vngcloud globalloadbalancer create-pool --load-balancer-id <glb-id> --name <name> --member-name <member-name> --backend-server-id <server-id> --backend-vpc-id <vpc-id> --backend-subnet-id <subnet-id> --backend-port <port> --project-id <project-id> --yes"
	docExampleOverride["globalloadbalancer create-listener"] = "vngcloud globalloadbalancer create-listener --load-balancer-id <glb-id> --name <name> --pool-id <pool-id> --port <port> --allowed-cidrs <cidr> --backend-server-id <server-id> --project-id <project-id> --yes"
	docExampleOverride["globalloadbalancer delete-listener"] = "vngcloud globalloadbalancer delete-listener --load-balancer-id <glb-id> --listener-id <listener-id> --yes"
	docExampleOverride["globalloadbalancer delete-pool-member"] = "vngcloud globalloadbalancer delete-pool-member --load-balancer-id <glb-id> --pool-id <pool-id> --pool-member-id <member-group-id> --yes"
	docExampleOverride["globalloadbalancer delete-pool"] = "vngcloud globalloadbalancer delete-pool --load-balancer-id <glb-id> --pool-id <pool-id> --yes"
	docOpNotes["globalloadbalancer create-pool"] = "Requires --yes. Step one of a Layer 4 setup: create the pool first, then `create-listener`. Adds one TCP pool with one private member group holding one vServer backend, on an existing Layer 4 load balancer that has no pool and no listener. " +
		"The backend address is the server's private fixed IP, proven from the server, VPC and subnet under `--project-id`, which must be the project of this client; a floating IP is never used and no raw address is accepted. " +
		globalLoadBalancerChildBilling + " " +
		"The create is sent once and never resent. Success needs exact read-back of one pool and its group, never traffic. The pool and its health monitor must be ACTIVE; member health is reported, not required, so a new backend shows OFFLINE until health checks pass, and UNHEALTHY if they cannot reach it. On NotSettled or WriteFailed the partial output on stdout holds only IDs; inspect inventory and billing before another mutation."
	docOpNotes["globalloadbalancer create-listener"] = "Requires --yes. Step two of a Layer 4 setup, after `create-pool`: add one TCP listener bound to the load balancer's only pool, which must be ACTIVE and have no listener; member health (ONLINE, OFFLINE, HEALTHY or UNHEALTHY) is reported, not required. " +
		"`--allowed-cidrs` is repeatable and required. Each value is a canonical IPv4 network from /8 through /32 with no host bits, with no repeats and no set covering all IPv4 addresses; prefer a /32 for a known client. The set is confirmed by exact read-back, but that does not prove the provider enforces it. " +
		globalLoadBalancerChildBilling + " " +
		"The create is sent once and never resent. On NotSettled or WriteFailed the partial output on stdout holds only IDs; inspect inventory and billing before another mutation."
	docOpNotes["globalloadbalancer delete-listener"] = "Requires --yes. Step one of cleanup, then `delete-pool-member`, `delete-pool`, and `delete-load-balancer`. Deletes the sole listener of a Layer 4 load balancer after re-reading it and its pool; the pool and member group stay, and the listener stops receiving traffic. " +
		"`Deleted` is true only after the listener is absent from the exact read and the list while its pool and the load balancer remain. Deleting does not promise a refund. On NotSettled the partial output holds the IDs; inspect inventory and billing before another mutation."
	docOpNotes["globalloadbalancer delete-pool-member"] = "Requires --yes. Step two of cleanup. Deletes the member group named by `--pool-member-id` from a pool that has no listener and exactly this one group; it never deletes the nested backend row's vServer or the pool. " +
		"`Deleted` is true only after the group is absent from the exact read and the list while the pool and the load balancer remain. Deleting does not promise a refund. On NotSettled the partial output holds the IDs; inspect inventory and billing before another mutation."
	docOpNotes["globalloadbalancer delete-pool"] = "Requires --yes. Step three of cleanup, before `delete-load-balancer`. Refuses, sending nothing, unless the pool is the load balancer's only pool, is stable, and has no listener and no member group; nothing cascades. " +
		"`Deleted` is true only after the pool is absent from the exact read and the list while the load balancer remains. Deleting does not promise a refund. On NotSettled the partial output holds the IDs; inspect inventory and billing before another mutation."
}
