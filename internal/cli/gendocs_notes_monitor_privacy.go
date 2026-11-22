package cli

const monitorWritePrivacyNote = "Suppresses response captures during channel and check writes, including " +
	"prerequisite and confirmation reads. Known credentials are withheld from error codes, messages, and " +
	"retained causes. Reflections outside documented secret fields make a response invalid. After a " +
	"write whose response was withheld, confirm the result with reads before deciding on another write."

const monitorCheckGateNote = "Within one SDK client, pause, resume, and update serialize calls for the " +
	"same check ID. Canceled waiters send nothing; unrelated check IDs can progress independently."
