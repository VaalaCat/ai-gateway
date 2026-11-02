# Image, video, and Decisions endpoints

The model relay exposes these provider-compatible HTTP endpoints using the same
AIGW API Token, model/channel selection, limits, and upstream credentials as chat:

| Method | Path | Body |
| --- | --- | --- |
| POST | `/v1/images/generations` | Existing image-generation API |
| POST | `/v1/images/edits` | JSON or multipart form with `model` |
| POST | `/v1/images/variations` | Multipart form with `model` |
| POST | `/v1/decisions` | Provider JSON with `model`, `input`, and `questions` |
| POST | `/v1/videos` | Provider JSON or multipart form with `model` |
| GET | `/v1/videos/{id}` | No body |
| GET | `/v1/videos/{id}/content` | No body; query parameters such as `variant` are forwarded |
| DELETE | `/v1/videos/{id}` | No body |
| POST | `/v1/videos/{id}/remix` | Provider JSON or multipart form |

The upstream must support each operation. A registered gateway route does not
make a particular model support that endpoint. Provider errors (including
unsupported models and operations) are returned through the normal relay error
path. Listing all upstream video jobs is deliberately not exposed: shared channel
credentials can contain jobs belonging to different AIGW users.

## Channel configuration

Use an OpenAI-compatible channel with its existing API key and base URL. For
ZenMux, the base URL is `https://zenmux.ai/api`; AIGW appends `/v1/decisions` or
`/v1/videos`. Add the actual provider model IDs to the channel's model list, or
configure aliases using model mapping. No generic API Service/Route is necessary.
These APIs use raw passthrough independently of the chat passthrough/legacy switch;
chat protocol conversions are not applied to image, video, or decision payloads.

Optional endpoint overrides in the channel's `endpoints` JSON use these keys:

```json
{
  "image_edits": "/v1/images/edits",
  "image_variations": "/v1/images/variations",
  "videos": "/v1/videos",
  "decisions": "/v1/decisions"
}
```

The `videos` override names the collection path; task IDs and `/content` or
`/remix` are appended automatically. Multipart uploads preserve files, masks,
repeated fields, and part headers while rewriting the model form field. Uploaded
files are replayable for retries and do not pass through a chat JSON codec.

## Decisions example

```bash
curl "$AIGW_URL/v1/decisions" \
  -H "Authorization: Bearer $AIGW_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "cloudflare/clef-flash",
    "input": "Hello, good morning!",
    "questions": [{
      "type": "predicate",
      "name": "greeting",
      "instructions": "Does the input contain a greeting?"
    }]
  }'
```

Questions, probabilities, confidence values, native options, and response fields
are preserved. Provider-reported token usage is used without synthesizing output
tokens for non-generative decisions.

## Image editing example

```bash
curl "$AIGW_URL/v1/images/edits" \
  -H "Authorization: Bearer $AIGW_API_KEY" \
  -F 'model=your-image-model' \
  -F 'prompt=Make the background blue' \
  -F 'image=@input.png' \
  -F 'mask=@mask.png'
```

## Video lifecycle

Create a video using the native payload expected by the selected provider:

```bash
curl "$AIGW_URL/v1/videos" \
  -H "Authorization: Bearer $AIGW_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "bytedance/doubao-seedance-2.0",
    "content": [{"type": "text", "text": "A blue ball rolling on a white tabletop"}],
    "resolution": "480p",
    "duration": 4,
    "generate_audio": false
  }'
```

Use the returned `id` unchanged in subsequent requests:

```bash
curl "$AIGW_URL/v1/videos/$VIDEO_ID" \
  -H "Authorization: Bearer $AIGW_API_KEY"
```

AIGW returns an opaque `video_aigw1_...` ID in place of the native provider ID.
The signed handle binds the task to its creating API Token, user (including
ownerless Tokens), channel source/ID, model, and upstream base URL. This prevents
cross-user task access and prevents polling from falling back to another channel.
The binding works across Agent restarts and load balancing without an in-memory
task map. Current model/channel permissions are checked again on every request.
All participating Agents must be upgraded to support these routes and the
extended bound-attempt method/query metadata.

Handles expire after seven days. Rotating the channel's upstream key or changing
its base URL invalidates existing handles; native IDs cannot be imported. Keep
using the same AIGW API Token for the lifetime of a video task. Downloads remain
binary streams; metadata responses are bounded to 2 MiB. Remix returns a new task
handle when the upstream implements it. ZenMux native tasks instead expose their
result URL in `content` when `status` becomes `succeeded`; AIGW preserves that
payload without downloading the video.

## Usage and billing boundary

Provider-reported token usage is recorded for generation/decision requests.
Polling, deleting, and downloading a video do not settle its generation tokens a
second time. Existing AIGW model prices are token-based: this change does not add
per-image, per-second, or asynchronous completion billing. Native video/image
fees remain charged by the upstream and must not be inferred from gateway token
costs. Enable these models only under a billing policy appropriate for that
limitation (for example BYOK or an explicitly free channel).

Provider references:
- https://zenmux.ai/docs/api/openai/openai-decisions.html
- https://zenmux.ai/docs/api/zenmux/generate-videos-native.html
