# Slash commands

Type a command in the composer instead of a prompt. Everything here is reachable from the command palette (`ctrl+k`) too; these exist because typing `/temp 0.2` beats opening a menu once you know what you want.

A message is a command only when it **starts** with `/`. Asking "what does 10/2 mean" is an ordinary prompt.

## The commands

| Command | Argument | Does |
| --- | --- | --- |
| `/model` | `<name>` | Switch the active model |
| `/system` | `<text>` | Set the system prompt, or clear it when given nothing |
| `/temp` | `<0-2>` | Set the temperature |
| `/seed` | `<n>` | Fix the seed for reproducible output; `0` is random again |
| `/think` | `on`\|`off` | Show or hide reasoning from models that expose it |
| `/prompt` | `<name>` | Load a saved prompt into the composer |
| `/save` | `<name>` | Save the last prompt to the library |
| `/forget` | `<name>` | Delete a saved prompt |
| `/preset` | `<name>` | Apply sampling settings; `/preset save <name>` keeps the current ones |
| `/host` | `<name\|url>` | Switch server, by saved name or a bare URL; `add <name> <url>` and `forget <name>` manage the list |
| `/find` | `<text>` | Search every conversation, not just the open one |
| `/tools` | `on`\|`off` | Let models call tools; with no argument, list them |
| `/theme` | `<name>` | Switch palette; with no name it cycles |
| `/clear` | | Start a new chat |
| `/export` | `md`\|`json`\|`html` | Write the conversation to a file, markdown by default |
| `/help` | | Open the keymap |

Every setting a command changes is the same one the Settings tab edits, and is saved the same way.

## Completion

The line under the composer lists the commands still matching what you have typed, from the moment you type `/`.

```
/            ->  /model · /system · /temp · /seed · /think · +11 more
/s           ->  /system · /seed · /save
/te          ->  /temp <0-2>  set temperature
```

A list too long for the line ends in a count rather than being cut off mid-name. `tab` completes an unambiguous command and leaves the cursor ready for its argument. While a command is being typed `tab` completes rather than moving focus between panes; with nothing to complete it moves focus as usual.

## The prompt library

`/save <name>` keeps the prompt you last sent, and `/prompt <name>` brings it back. Saved prompts also appear in the command palette, which is where they are easiest to find.

The library lives in `prompts.json` beside the sessions, and `/prompt` with no name lists it.

`/save` takes the **last prompt sent**, not what is in the composer: the composer holds the `/save` command itself at that moment. A selected message cannot be the source either, since a selection is dropped as soon as the keyboard returns to the composer.

### Blanks

A saved prompt may leave blanks as `{{name}}`:

```
Review this {{language}} code for {{concern}}
```

Loading it names the blanks under the composer, and sending while any remain is refused - finding out from the reply would cost a whole generation. Fill them in and send as usual.

## Presets

`precise`, `balanced` and `creative` always exist, from most predictable to least. `/preset save <name>` keeps whatever the sliders are set to now, and a saved preset shadows a built-in of the same name.

A preset covers only what changes the writing - temperature, top_p, top_k and the repeat penalty. The context size, model and system prompt are left alone. The Settings tab names the preset in force whenever the numbers match one.

## Matching a model

`/model` accepts a fragment rather than the full name:

```
/model qwen3     ->  huihui_ai/qwen3-abliterated:30b-a3b
```

An exact name always wins. Otherwise the fragment has to match exactly one installed model - `/model 3b` against both `llama3.2:3b` and `qwen3-abliterated:30b-a3b` is refused rather than resolved arbitrarily.

## Command highlighting

The composer changes colour once what you have typed is a command that exists:

```
/the          plain - no such command yet
/theme        tinted - recognised
/theme ember  tinted - still recognised, now with an argument
/mdoel        plain - mistyped, and visibly so before you send it
```

The colour appearing is the confirmation. A half-typed prefix has not earned it, and neither has a typo - which is how a mistyped command is caught before enter rather than after.

## Mistakes

An unknown command is refused, not sent:

```
/mdoel llama3    ->  no such command: /mdoel
```

Learning about a typo from the model's reply to it is a poor way to find out, and it would leave the typo in the conversation's history.

## Adding one

`slashCommands` in [`internal/ui/slash.go`](../internal/ui/slash.go) is a plain table:

```go
{"temp", "<0-2>", "set temperature", func(a *App, arg string) tea.Cmd {
    v, err := strconv.ParseFloat(arg, 64)
    if err != nil || v < 0 || v > 2 {
        return a.showToast("temperature is a number from 0 to 2", true)
    }
    a.cfg.Temperature = v
    _ = a.cfg.Save()
    return a.okToast(fmt.Sprintf("temperature %.2f", v))
}},
```

Completion, the hint line and the refusal of unknown commands all read from that table, so a new entry needs nothing else. An empty `arg` marks a command that takes none, which is what tells completion not to leave a trailing space.
