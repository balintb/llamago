# Tools

A tool lets a model do something instead of only describing it: read a file, list a directory, fetch a page. The model asks for the call, llamago runs it and hands back the result, and the model carries on with the answer.

**Anyone can add a tool without touching the source.** A tool is a JSON file in `~/.config/llamago/tools/` naming a program to run. The built-in ones are written in Go, but they go through the same interface, so nothing about a tool depends on which kind it is.

## What the model sees

Only models advertising the `tools` capability get any of this; the Models tab shows which.

The flag is not a promise. A model can advertise tools and still write the call into its answer instead of asking for one, because it was trained on a different call format than the template parses back. `phi4-mini` does exactly this:

```
To list the files, I can use the "list_files" function.

[{"name": "list_files", "arguments": {"path": "" }}]
```

Nothing runs - that is text, not a request - and the model then carries on as though it had the answer, inventing filenames. llamago recognises a reply like this and says so underneath it, naming a model that works, rather than leaving you with a wall of JSON that looks like a bug here.

## Built-in tools

| Tool | Does | Asks first |
| --- | --- | --- |
| `read_file` | Read a text file | no |
| `list_files` | List a directory | no |
| `find_files` | Find files by name under a directory | no |
| `file_info` | Size, kind, age, permissions, line count | no |
| `now` | The current date and time | no |
| `http_get` | Fetch a URL as text | yes |

The first five only read, and only below the directory llamago was started in, so they run without interrupting. `http_get` leaves the machine, so it asks.

`file_info` is the one to reach for before reading anything:

```
path: README.md
kind: file
modified: 2026-08-13 17:56:42  (6 minutes ago)
permissions: -rw-r--r--
size: 7.2 KB  (7379 bytes)
contents: text, 189 lines
```

It answers the questions that decide whether reading is worth it - is this text or a binary, how big, how old - and reports a symbolic link as itself rather than as whatever it points at.

Deliberately absent: anything that runs a shell command. That belongs in a tool you declare yourself, where the decision to allow it is explicit.

## Switching tools off

The Settings tab lists every tool with a checkbox, built-in or declared. A tool is on unless you switch it off, so one installed later does not need finding and enabling first. A tool switched off is not offered to the model, and is refused if a model asks for it anyway from something it saw earlier in the conversation.

## Adding your own

Drop a `.json` file in `~/.config/llamago/tools/`:

```json
{
  "name": "weather",
  "description": "Current weather for a city. Use for questions about weather.",
  "parameters": {
    "type": "object",
    "properties": {
      "city": { "type": "string", "description": "City name, e.g. Lisbon" }
    },
    "required": ["city"]
  },
  "command": ["/usr/local/bin/weather", "--city", "{{city}}"],
  "timeout": "10s"
}
```

- `name`, `description` and `parameters` are what the model is shown. `description` is the most important field you write: it is how the model decides whether to call the tool at all.
- `parameters` is JSON Schema. Keep it small - a few named strings and numbers.
- `command` is argv, not a shell line. `{{name}}` is replaced by the argument of that name, always as one whole argv element.
- `timeout` defaults to 30s.
- `stdin: true` sends the arguments as a JSON object on standard input instead of substituting them into argv.
- `safe: true` marks a tool that only reads and may run without asking. Leave it out and every call asks first.
- `ok_exit` lists exit codes that are not failures. Several standard tools report "found nothing" as a non-zero exit, which is an answer rather than an error.
- `dir` is where the program runs. Left out, it inherits the directory llamago was started in, which is what a tool working on the project in front of you wants.

Standard output is the result. A non-zero exit is an error, and standard error is what the model is told went wrong.

### A worked example

The built-in tools find files by name but cannot search their contents. `grep` can, and it is on every macOS and Linux machine. Save this as `~/.config/llamago/tools/search_code.json`:

```json
{
  "name": "search_code",
  "description": "Search the working directory for a pattern and return matching lines with their file and line number. Use when you need to find where something is written rather than guess.",
  "parameters": {
    "type": "object",
    "properties": {
      "pattern": { "type": "string", "description": "Text or basic regular expression to search for" },
      "path":    { "type": "string", "description": "Directory to search under. Defaults to the working directory." }
    },
    "required": ["pattern"]
  },
  "command": ["grep", "-rn", "--max-count=5", "--exclude-dir=.git", "{{pattern}}", "{{path}}"],
  "ok_exit": [0, 1],
  "safe": true,
  "timeout": "15s"
}
```

Three details are doing real work:

- **`ok_exit: [0, 1]`** - grep exits 1 when it matches nothing. Without this the model would be told the search failed, and would apologise instead of concluding the thing is not there.
- **`--max-count=5`** - one unlucky pattern can otherwise return a thousand lines and fill the context window with a single call.
- **`safe: true`** - it only reads, so it runs without stopping to ask. Leave this out for anything that writes, deletes or sends.

The `description` is the part worth spending time on. It is the whole basis on which the model decides to call the tool, and "Search for a pattern" earns far fewer calls than a sentence saying when to reach for it.

### Why no shell

`command` is executed directly. There is no shell, so no quoting, globbing or `;` to get wrong, and a model that asks for a city called `"; rm -rf ~"` gets a program looking for a city with a strange name rather than a deleted home directory.

## Permission

Every call to a tool that is not `safe` asks first, naming the tool and showing the arguments. Allowing it once allows it for that conversation; the answer is not remembered beyond it.

`tools` in the config turns the whole mechanism off, and it is off until you turn it on.

## The loop

A reply asking for tools is not the end of the turn: the calls run, the results go back, and the model answers again, up to `tool_steps` rounds (5 by default). The cap is what stops a model that keeps calling the same tool from doing so forever.

Calls and their results are stored as their own turns, so a conversation reopened later still shows what was run and what came back, and an export carries them too.

The history shows what was asked for and what answered it, not the output:

```
● qwen3-abliterated
⚒ list_files(path=internal)
↳ list_files  path=internal  12 lines  → show
```

`⚒` is the model asking, `↳` is what came back. Select the result with `shift+↑` and `→` shows it, `←` hides it again; a long call's arguments unfold the same way. Nothing is shown by default, since one file read would otherwise bury the conversation it was part of - and the model receives the whole result either way, whatever is on screen.

## When nothing happens

Two things have to be true before a tool can run, and the header says whether they are:

```
llama3.2:3b · 4k ctx · ⚒ 5             five tools, ready
qwen2.5vl:3b · 4k ctx · ⚒ unsupported  this model cannot call tools
qwen2.5vl:3b · 4k ctx                  tools are switched off
```

A model without the capability will answer a question about the time by saying it cannot know, which looks like a broken feature and is not one: it was never offered the tool. The Models tab lists which of your models advertise `tools`, and the note beside the Settings heading names one to switch to.
