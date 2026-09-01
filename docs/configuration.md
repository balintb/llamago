# Configuration

Everything lives in `$XDG_CONFIG_HOME/llamago`, defaulting to `~/.config/llamago`:

```
~/.config/llamago/
  config.json          settings
  prompts.json         the saved prompt library
  sessions/*.json      one file per conversation
  exports/*            markdown, HTML and JSON exports
  attachments/*        images, named by content digest
```

Settings tab shows where its own config lives, at the bottom of the pane.

## Inference

These are the knobs the Settings tab adjusts with `←`/`→`:

| Field | Default | Does |
| --- | --- | --- |
| `temperature` | `0.7` | Randomness. `0` is deterministic, above `1` gets loose |
| `top_p` | `0.9` | Nucleus sampling cutoff |
| `top_k` | `40` | How many candidate tokens to consider |
| `repeat_penalty` | `1.1` | Discourages the model from repeating itself |
| `num_ctx` | `4096` | Context window in tokens; larger costs memory |
| `num_predict` | `-1` | Cap on generated tokens; `-1` means no cap |
| `seed` | `0` | Fix for reproducible output. `0` is Ollama's "roll a new one per request" and is left out of the request entirely |
| `stop` | none | Sequences that halt generation, matched literally |
| `keep_alive` | `5m` | How long a model stays resident after use |

`num_ctx` is the one worth attention: the context meter under the composer warms to amber and then red as a conversation approaches it, because past it Ollama silently drops the oldest turns.

### Presets

`presets` holds sampling parameters saved by name. `precise`, `balanced` and `creative` always exist; a saved preset of the same name shadows the built-in.

```json
"presets": {
  "mine": { "temperature": 0.4, "top_p": 0.9, "top_k": 20, "repeat_penalty": 1.1 }
}
```

Model, context size and system prompt are left alone. `/preset save <name>` writes one from the current settings, and the Settings tab names the preset in force whenever the numbers match one.

## Behaviour

| Field | Default | Does |
| --- | --- | --- |
| `think` | `true` | Ask reasoning-capable models to expose their thinking |
| `markdown` | `true` | Render responses as rich markdown |
| `timestamps` | `false` | Show when each message was sent |
| `resume` | `false` | Reopen the last conversation on startup |
| `auto_title` | `false` | Let the model name a conversation after its first exchange |
| `theme` | `midnight` | Palette: `midnight`, `daylight` or `ember` |
| `tools` | `false` | Let models call [tools](tools.md) |
| `tool_steps` | `5` | Rounds of tool calling allowed in one turn |
| `tools_off` | none | Tools switched off by name, from the Settings checkboxes |

`think` is sent explicitly for models that support it. Left unset, a reasoning model thinks whether or not you wanted it to.

Reasoning streams as it arrives and folds to a single line the moment the answer starts - it is finished by then, and it is working-out rather than answer, usually longer than what it produced. `→` on the selected message shows it again; `think` off stops the model producing it at all.

`auto_title` costs one short request per new conversation, bounded by a timeout and silent if it fails. It only ever titles a conversation still carrying the title derived from its first prompt, so a name you chose is never overwritten.

## Model and prompt

| Field | Default | Does |
| --- | --- | --- |
| `model` | first available | The model in use |
| `system` | none | Sent as the system message at the start of every conversation |

A conversation records the system prompt in force when it starts and keeps using it, so editing the global one later does not change the persona of a thread already under way. The header flags a conversation whose prompt has diverged. Sessions started with no prompt keep following the global setting.

## Servers

`host` is the Ollama server in use, and `hosts` names the ones worth switching between:

```json
"host": "http://127.0.0.1:11434",
"hosts": [
  { "name": "laptop", "url": "http://127.0.0.1:11434" },
  { "name": "workstation", "url": "http://10.0.0.5:11434" }
]
```

`/host <name>` switches, `/host add <name> <url>` saves one. Switching drops the models, memory, cached details and version from the old server: all of it describes one machine.

Left empty, `host` follows `$OLLAMA_HOST`, then falls back to `http://127.0.0.1:11434`. The `-host` flag overrides both for one run.

## Images

| Field | Default | Does |
| --- | --- | --- |
| `graphics` | `auto` | Image protocol: `auto`, `kitty`, `iterm2`, `sixel` or `none` |
| `save_dir` | `~/Downloads` | Where "save image as" starts |

Detection reads the terminal's own environment and is usually right. Override it when a terminal claims a protocol it does not really support, or set `none` to stay with the inline half-block thumbnails, which work everywhere.

## Flags

Flags apply to one run and are not written back:

```
-host string     Ollama host
-model string    model to start with
-system string   system prompt for this run
-version         print version and exit
```

## Anything else

`prompts.json` is the [prompt library](slash-commands.md#the-prompt-library), written by `/save` rather than by hand.

`models.json` extends the list the pull picker offers. Ollama publishes no API for browsing its library, so that list ships with llamago and would otherwise go stale with the release it came in:

```json
[
  { "name": "brand-new:12b", "size": "~8 GB", "purpose": "published last week",
    "tags": ["tools"] }
]
```

An entry naming a built-in replaces it rather than appearing twice, so a size or a description can be corrected as well as added to.

Sessions are one JSON file each, named by the timestamp they were created at, and are the same shape `/export json` writes - so an export can be dropped back into `sessions/` and reopened.
