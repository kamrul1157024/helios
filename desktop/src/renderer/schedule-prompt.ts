/**
 * The prompt that asks an agent to write a schedule.
 *
 * Nobody wants to fill in a cron field. The usual way to make a schedule is to
 * say what you want, and let an agent that already knows the CLI turn it into
 * one — the `helios` skill, installed during agent setup, is the manual it
 * reads. The form stays for the times you want to be exact, and for when there
 * is no agent to ask.
 *
 * Deliberately short. The skill carries the flags, the rules and the examples;
 * repeating them here would give the agent two manuals that can disagree.
 */
export function schedulePrompt(
  description: string,
  cwd: string,
  // Picked in the form rather than left to the agent to guess, and written as
  // the flags the CLI takes, because running that CLI is what the agent does
  // next.
  options: { model?: string; mode?: string } = {},
): string {
  return [
    'Create a Helios schedule from this description:',
    '',
    description.trim(),
    '',
    'Use the helios skill and the `helios schedule` CLI. Work out which kind it is —',
    'a timer, a one-shot, a monitor with a check, or a job that runs after another —',
    'and create it with a name that reads well in a list.',
    cwd ? `Unless the description says otherwise, it should run in ${cwd}.` : '',
    options.model ? `Give it --model ${options.model}.` : '',
    options.mode ? `Give it --permission-mode ${options.mode}.` : '',
    '',
    'Then run `helios schedule list` and tell me in one or two lines what you made,',
    'when it next fires, and anything you had to guess.',
  ]
    .filter((line) => line !== '')
    .join('\n')
}

/** The same, for a change to one that already exists. */
export function scheduleEditPrompt(name: string, description: string): string {
  return [
    `Change the Helios schedule called "${name}":`,
    '',
    description.trim(),
    '',
    'Use the helios skill and `helios schedule edit`. Then show me the result with',
    '`helios schedule list` and say what changed.',
  ].join('\n')
}
