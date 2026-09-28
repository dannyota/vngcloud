package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/tagging"
)

// taggingOps is tagging's operation table. TagResource and UntagResource are
// Write but not Destructive: each replaces only the resource's user tag
// list, and TagResourceOutput's and UntagResourceOutput's own Previous field
// lets a caller undo the write by setting Key back to it, so neither needs
// --yes. Every field of all three Inputs gets a flag from flags.go's
// reflection; none needs NoFlag.
var taggingOps = []Op[tagging.Client]{
	Read[tagging.Client, tagging.ListResourceTagsInput, tagging.ListResourceTagsOutput](
		kebab("ListResourceTags"), (*tagging.Client).ListResourceTags),
	Write[tagging.Client, tagging.TagResourceInput, tagging.TagResourceOutput](
		kebab("TagResource"), (*tagging.Client).TagResource),
	Write[tagging.Client, tagging.UntagResourceInput, tagging.UntagResourceOutput](
		kebab("UntagResource"), (*tagging.Client).UntagResource),
}

func newTaggingCmd(e *env) *cobra.Command {
	return Service(e, "tagging", "Resource tags, any resource type", tagging.New, taggingOps...)
}
