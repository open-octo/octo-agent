// A slash command is a whole-message prefix, so text already typed in the
// composer when the "/" menu opens is the command's argument, not something to
// throw away — picking a skill used to overwrite the whole draft.
export function composeSlashCommand(command: string, draft: string): string {
  const tail = draft.trim()
  if (!tail) return command
  return command.endsWith(' ') ? command + tail : command + ' ' + tail
}
