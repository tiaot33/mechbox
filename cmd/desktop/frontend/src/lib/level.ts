// 提醒的三级，从重到轻：严重(critical)、注意(warn)、提示(info)。各级什么时候用、长什么样见 components/ui/badge。
// 一处有几条提醒时只画最重的一级：先后和「取最重」都只在这里定。
export const levels = ["critical", "warn", "info"] as const;

export type Level = (typeof levels)[number];

// Mark 是有图标的几种：三级提醒，加上不算提醒的「更新」(有新版本、可能改名),它用主题绿的图标，不和三级混在一起。
export type Mark = Level | "update";

// Tone 是标签的样子：有图标的几种，加上只是信息的 plain。
export type Tone = Mark | "plain";

export function worstLevel(tones: readonly (Tone | undefined)[]): Level | undefined {
  return levels.find((l) => tones.includes(l));
}

// 先后：三级提醒从重到轻，更新排在最后(更新另画一个图标，和提醒放在一起时也不盖住提醒)。
const order: readonly Mark[] = [...levels, "update"];

export function byLevel(a: Mark, b: Mark): number {
  return order.indexOf(a) - order.indexOf(b);
}
