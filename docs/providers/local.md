# Local (Self-Hosted) Insights Provider

The `local` provider enables self-hosted transcription and translation
without sending audio to a cloud vendor. It is useful for GDPR-sensitive
deployments, air-gapped environments, or cost-sensitive operators.

## Architecture

```
LiveKit audio ──► plugNmeet-server ──► local provider
                                          │
                                          ├─ WebSocket (PCM16 16kHz mono)
                                          │
                                          ▼
                             companion service (Python)
                              ├─ faster-whisper (STT)
                              └─ NLLB-200 (translation, optional)
```

The `local` provider is a thin Go client that speaks a simple WebSocket
protocol. The heavy lifting (speech recognition, translation) happens in a
separate companion service written in Python. The reference
implementation is available at:

**<https://github.com/xynstr/plugnmeet-local-insights>** (MIT licensed)

## Configuration

Add this block to your `config.yaml`:

```yaml
insights:
  enabled: true
  providers:
    local:
      - id: "local-01"
        credentials:
          api_key: ""
          region: ""
        options:
          whisper_url: "ws://whisper-local:8002/ws/transcribe"
          translate_url: "http://whisper-local:8002/translate"
  services:
    transcription:
      provider: "local"
      id: "local-01"
      options: {}
    translation:
      provider: "local"
      id: "local-01"
      options: {}
```

`whisper_url` is the WebSocket endpoint of the companion service.
`translate_url` is optional and only needed when translation is used.

## Running the companion service

```bash
docker run -d --name whisper-local \
  --network plugnmeet_net \
  -p 8002:8002 \
  ghcr.io/xynstr/plugnmeet-local-insights:latest
```

Or build from source — see the companion repo's README for details.

## Supported languages

Transcription (via faster-whisper):
`de`, `en`, `ar`, `uk`, `ru`, `fr`, `es`, `it`, `pl`, `tr`, `fa`, `zh`,
`ja`, `ko`, `pt`, `nl`.

Translation (via NLLB-200, optional, non-commercial license — see
companion repo for details): same list.

## Hardware notes

The reference implementation defaults to CPU with int8 quantization.
It is tested on:

- ARM64 (Neoverse-N1, 10 cores) — `small` Whisper model, real-time
  transcription feasible with VAD and 500 ms chunks.
- x86_64 — similar performance profile.

For GPU, switch the companion service to `device=cuda` via environment
variables (see companion repo).

## Protocol

The WebSocket protocol is intentionally minimal:

```
Client → Server  {"type":"start","lang":"de","transLangs":["en"]}
Client → Server  <binary PCM16 audio frames>
Server → Client  {"type":"partial","text":"...","lang":"de"}
Server → Client  {"type":"final","text":"...","lang":"de"}
Server → Client  {"type":"error","error":"..."}
Client → Server  {"type":"end"}
```

Anyone implementing a different backend (e.g., whisper.cpp, Vosk, Deepgram
self-hosted) can replace the companion service without changing any Go
code, as long as this protocol is honored.
