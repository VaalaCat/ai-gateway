package consts

// Channel provider types (numeric IDs used in database).
// Values match new-api's constant.ChannelType* constants.
const (
	ChannelTypeOpenAI    = 1
	ChannelTypeAzure     = 3
	ChannelTypeOllama    = 4
	ChannelTypeAnthropic = 14
	ChannelTypeGemini    = 24
	ChannelTypeAWS       = 33
	ChannelTypeVertexAI  = 41
)
