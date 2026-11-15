// 提醒的三级，从重到轻：严重(critical)、注意(warn)、提示(info)。各级什么时候用、长什么样见 components/ui/badge。
// 一处有几条提醒时只画最重的一级：先后和「取最重」都只在这里定。
export const levels = ["critical", "warn", "info"] as const;

export type Level = (typeof levels)[number];

// Tone 是标签的四种样子：三级提醒，加上只是信息的 plain。
export type Tone = Level | "plain";

export function worstLevel(tones: readonly (Tone | undefined)[]): Level | undefined {
  return levels.find((l) => tones.includes(l));
}

export function byLevel(a: Level, b: Level): number {
  return levels.indexOf(a) - levels.indexOf(b);
}
