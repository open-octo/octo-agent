// A slash command is a whole-message prefix, so text already typed in the
// composer when the "/" menu opens is the command's argument, not something to
// throw away — picking a skill used to overwrite the whole draft.
export function composeSlashCommand(command: string, draft: string): string {
  const tail = draft.trim()
  if (!tail) return command
  // A "/name " prefill takes the draft as its argument. The natural-language
  // prefills (workflow, MCP tool) are whole sentences, so the draft goes on its
  // own line rather than running into the end of one.
  return command.endsWith(' ') ? command + tail : command + '\n\n' + tail
}
