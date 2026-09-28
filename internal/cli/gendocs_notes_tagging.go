package cli

// tagging's own doc notes, kept apart from gendocs_notes.go so neither file
// grows past the length limit; docOpNotes in that file keys each constant
// below by "tagging <op-name>", and docServiceIntro (gendocs_notes_iam.go)
// keys taggingServiceIntroNote by "tagging".

// taggingServiceIntroNote documents the one fact that holds for every
// tagging operation: one API on the vServer gateway serves every resource
// type, so ListResourceTags, TagResource, and UntagResource all take a
// ResourceID alone, with no resource-specific command of their own.
const taggingServiceIntroNote = "One tag API on the vServer gateway serves every resource type: there is no " +
	"per-resource tag command, only ResourceID."

// taggingListResourceTagsNote documents that the read returns system tags
// too, since the flag table shows only --resource-id with no hint that the
// result can include tags this CLI's own writes can never set or remove.
const taggingListResourceTagsNote = "Returns every tag on the resource, system tags included: vng.zone, " +
	"vng.region, and vng.createdBy are confirmed live on a virtual IP address. Tag.SystemTag marks one of them."

// taggingSystemTagGuardNote documents the guard both tag-resource and
// untag-resource run before any write: the flag table shows --key as a
// plain, required string, with no hint that some values are refused outright.
const taggingSystemTagGuardNote = "Refuses, before any write, with error code SystemTag, when --key starts " +
	"with vng. or already names an existing system tag on the resource: the platform's own tags (vng.zone, " +
	"vng.region, and vng.createdBy, confirmed live on a virtual IP address) are never sent and can never be set " +
	"or removed through this command."

// taggingWriteShapeNote documents the read-merge-send-confirm shape both
// writes share, and the CLI's own NotSettled handling of a failed or
// mismatched confirm read: the flag table gives no hint that this command
// sends more than one request, or that a failure here still prints Tags.
const taggingWriteShapeNote = "Reads every tag on the resource, replaces its whole user tag list with the " +
	"change, then reads it again to confirm: the PUT never includes a system tag, since it replaces only the " +
	"user tag list. A mismatched or failed confirm read is error code NotSettled; the write already reached the " +
	"server, so the CLI still prints the last tags a read returned on stdout, and the tags should be read again " +
	"before writing once more, rather than repeating this command blind."

// taggingNoYesNote documents why tag-resource and untag-resource need no
// --yes, since neither is marked Destructive in taggingOps and the flag
// table gives no reason for that on its own.
const taggingNoYesNote = "Needs no --yes: the write's own Previous field names Key's value before the change " +
	"(empty when it was absent), so the change can be undone with one more tag-resource or untag-resource call."

// taggingResourceTypeNote documents how --resource-type reaches the server,
// and which values are confirmed or merely named, since the flag table
// shows it as a plain required string with no further detail.
const taggingResourceTypeNote = "--resource-type is sent to the server exactly as given. VIRTUAL-IP-ADDRESS is " +
	"confirmed live to accept a tag write for free; SERVER, VOLUME, and LOAD-BALANCER are named by VNG Cloud's " +
	"own SDK for this same call, on paid resources this CLI has not tried."

// taggingWriteNote assembles tag-resource's or untag-resource's full note
// from the shared paragraphs above plus noOp, the one paragraph that differs
// between them: what happens when the named change is already true.
func taggingWriteNote(noOp string) string {
	return taggingSystemTagGuardNote + " " + noOp + "\n\n" + taggingWriteShapeNote + "\n\n" +
		taggingNoYesNote + "\n\n" + taggingResourceTypeNote
}

// taggingTagResourceNote documents tag-resource's own no-op case, which
// taggingWriteNote's shared text does not cover.
var taggingTagResourceNote = taggingWriteNote("Setting --key to a --value it already holds among the user " +
	"tags is a no-op: Changed is false and nothing is sent.")

// taggingUntagResourceNote documents untag-resource's own no-op case, which
// taggingWriteNote's shared text does not cover.
var taggingUntagResourceNote = taggingWriteNote("Removing a --key already absent from the user tags is a " +
	"no-op: Changed is false and nothing is sent.")
