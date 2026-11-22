package cli

const taggingServiceIntroNote = "One tag API on the vServer gateway serves every resource type: there is no " +
	"per-resource tag command, only ResourceID."

const taggingListResourceTagsNote = "Returns every tag on the resource, system tags included: vng.zone, " +
	"vng.region, and vng.createdBy are confirmed live on a virtual IP address. Tag.SystemTag marks one of them."

const taggingSystemTagGuardNote = "Refuses, before any write, with error code SystemTag, when --key starts " +
	"with vng. or already names an existing system tag on the resource: the platform's own tags (vng.zone, " +
	"vng.region, and vng.createdBy, confirmed live on a virtual IP address) are never sent and can never be set " +
	"or removed through this command."

const taggingWriteShapeNote = "Requires an explicit tag array with each key, value, and system-tag marker, " +
	"and refuses missing fields, duplicate keys, and conflicting JSON before any PUT. Reads every tag, " +
	"merges the requested change, and sends the full user tag list once, without system tags. Redirects, " +
	"authentication failures, and server errors cannot replay the replacement. An uncertain send or a failed " +
	"confirmation returns NotSettled with the last tags, Previous, and Changed=true. Changed records an " +
	"attempt, not confirmed application. Read and reconcile tags before another write."

const taggingNoYesNote = "Needs no --yes: the write's own Previous field names Key's value before the change " +
	"(empty when it was absent), so the change can be undone with one more tag-resource or untag-resource call."

const taggingResourceTypeNote = "--resource-type is sent to the server exactly as given. VIRTUAL-IP-ADDRESS is " +
	"confirmed live to accept a tag write for free; SERVER, VOLUME, and LOAD-BALANCER are named by VNG Cloud's " +
	"own SDK for this same call, on paid resources this CLI has not tried."

func taggingWriteNote(noOp string) string {
	return taggingSystemTagGuardNote + " " + noOp + "\n\n" + taggingWriteShapeNote + "\n\n" +
		taggingNoYesNote + "\n\n" + taggingResourceTypeNote
}

var taggingTagResourceNote = taggingWriteNote("Setting --key to a --value it already holds among the user " +
	"tags is a no-op: Changed is false and nothing is sent.")

var taggingUntagResourceNote = taggingWriteNote("Removing a --key already absent from the user tags is a " +
	"no-op: Changed is false and nothing is sent.")
