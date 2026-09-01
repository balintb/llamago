# Keys

`f1` shows this inside the app, and `ctrl+k` searches every command by name. The keymap below is the same list the help draws from.

Keys are grouped by what has the keyboard. `tab` moves between the composer, the transcript and the session list - or click a pane to give it the keyboard - and the status bar always names the keys that are live where you are.

The mouse works where it is obvious: clicking a tab switches to it, clicking a pane focuses it, clicking a session selects that row, the wheel scrolls whatever is under the pointer, and clicking an image in the transcript offers to save it.

## Global

| Key | Does |
| --- | --- |
| `ctrl+k` | Command palette |
| `alt+1`…`alt+4` | Jump to a tab |
| `ctrl+o` | Next tab |
| `esc` | Step outward, ending at the tab strip |
| `ctrl+r` | Refresh from the server |
| `f1` | This keymap |
| `ctrl+q` | Quit |

`esc` never does anything surprising: in a chat it clears a search, then returns to the composer, and only when it has nothing left to do does it hand the keyboard to the tab strip. There `←`/`→` switch tabs, a digit jumps to one, and `↵` drops back into it.

## Chat

| Key | Does |
| --- | --- |
| `↵` | Send |
| `alt+↵` | Newline |
| `shift+⌫` | Clear the composer (shift+backspace) |
| `ctrl+u` | Delete back to the start of the line |
| `↑` / `↓` | Previous / next prompt |
| `ctrl+c` | Stop generating |
| `ctrl+n` | New chat |
| `ctrl+e` | Regenerate |
| `alt+e` | Regenerate with a nudge |
| `ctrl+y` | Copy the last response |
| `ctrl+i` | Attach an image |
| `ctrl+t` | Inline a text file |
| `ctrl+f` | Find in this conversation |
| `ctrl+a` | Widen that find to every conversation |
| `ctrl+s` | Export to markdown |
| `ctrl+\` | Race two models on this prompt |
| `ctrl+g` | Show or hide reasoning |
| `/tools` | Let models call [tools](tools.md) |
| `ctrl+b` | Show or hide the sidebar |
| `tab` / `shift+tab` | Next / previous pane |

`shift+⌫` (shift+backspace) empties the composer wherever the cursor sits. Terminals that cannot tell it from plain backspace - most, unless they speak the Kitty keyboard protocol - still have `ctrl+u`, which clears everything before the cursor and so empties the box whenever the cursor is at the end, which after typing it is.

`↑` only recalls a previous prompt from the composer's top row, so it still moves the cursor inside a draft of several lines. `↓` does nothing until recall has started, so it cannot wipe something half-typed.

`ctrl+a` belongs to the find bar rather than to the composer: it takes the query already typed there and looks through every conversation with it. `/find <text>` does the same from a standing start.

Commands typed into the composer have [their own page](slash-commands.md).

## Transcript

`tab` to the transcript first.

| Key | Does |
| --- | --- |
| `shift+↑` / `shift+↓` | Select a message |
| `y` | Copy the selected message |
| `Y` | Copy the whole conversation |
| `↵` | Put that prompt back in the composer |
| `r` | Ask again from here, dropping what follows |
| `f` | Fork into a new chat up to here |
| `x` | Delete the exchange |
| `→` / `←` | Show / hide a tool result, long arguments or reasoning |
| `m` | Raw text instead of rendered markdown |
| `1`…`9` | Copy the code block with that label |
| `v` / `o` | View / open the image |
| `j` / `k` | Move a line |
| `d` / `u` | Half a page |
| `g` / `G` | Top / bottom |
| `/` | Find |
| `n` / `N` | Next / previous match |

`shift+↑` selects from the composer without a detour through `tab`, and `shift+↓` past the newest message hands the keyboard back to it. Anything that drops turns - `r` and `x` - asks first when more than the last exchange is at stake, since there is no undo.

## Sessions

`tab` to the session list.

| Key | Does |
| --- | --- |
| `↵` | Open |
| `n` | New chat |
| `r` | Rename |
| `p` | Pin to the top |
| `c` | Duplicate |
| `d` | Delete |

## Compare

`ctrl+\` with a prompt typed starts a race.

| Key | Does |
| --- | --- |
| `↵` | Ask every column |
| `alt+a` | Add another model to the race |
| `tab` | Between the composer and the columns |
| `↵` on a column | Keep that thread and leave |
| `y` | Copy that column |
| `1` / `2` | Keep the first or second column |
| `ctrl+f` | Find across the columns |
| `ctrl+\` | Leave |

Each column keeps its own thread, so a follow-up continues from what that model actually said. How many columns fit depends on the window: below 34 cells each, another model is refused rather than shrinking them all, and four is the most any window holds.

The column keys need a column to be focused - `tab` first, then `↵` or `y`. `1` and `2` are shorthand for keeping those columns, not a way to move between them.

## Models

| Key | Does |
| --- | --- |
| `↵` | Chat with it |
| `p` | Pull a model: pick from the list or type a name |
| `d` | Delete it |
| `u` | Unload it from memory |
| `y` | Copy its name |
| `/` | Filter the list |
| `ctrl+d` / `ctrl+u` | Scroll the detail card |
| `j` / `k`, `g` / `G` | Move |

## Running

| Key | Does |
| --- | --- |
| `u` | Unload |
| `↵` | Chat with it |
| `j` / `k`, `g` / `G` | Move |

## Settings

| Key | Does |
| --- | --- |
| `←` / `→` | Adjust the selected field |
| `shift+←` / `shift+→` | Bigger steps |
| `↵` | Edit a field that opens an editor |
| `e` / `s` | System prompt / stop sequences |
| `r` | Reset to defaults |
| `↑` / `↓` | Move between fields |
| `g` / `G` | Top / bottom |

`↓` past the last field scrolls the connection block into view, which holds nothing selectable. `↑` always moves the selection and lets the view follow.

Every setting is saved as you change it - see [configuration](configuration.md).
