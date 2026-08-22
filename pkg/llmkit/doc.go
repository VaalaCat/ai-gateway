// Package llmkit provides three public entry points for LLM access:
// construct protocol-neutral Request and Event IR directly, use Codec to
// translate raw protocol payloads, or use Client for end-to-end HTTP calls.
// Stream failures that happen after Client.Call returns are delivered as an
// EventError followed by channel close. ErrorStageStream is available for
// stream-stage failures surfaced before a channel is returned; it does not
// introduce a second error channel.
package llmkit
