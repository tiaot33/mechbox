// 提醒的三级，从重到轻：严重(critical)、注意(warn)、提示(info)。各级什么时候用、长什么样见 components/ui/badge。
// 一处有几条提醒时只画最重的一级：先后和「取最重」都只在这里定。
export const levels = ["critical", "warn", "info"] as const;

export type Level = (typeof levels)[number];

// Mark 是有图标的几种：三级提醒，加上不算提醒的「更新」(有新版本)与「可能改名」,它们用主题绿的图标，不和三级混在一起;
// 可能改名要用户确认是不是同一个技能，和有新版本不是一回事，图标的形状不同(向右的箭头)。
export type Mark = Level | "update" | "rename";

// Tone 是标签的样子：有图标的几种，加上只是信息的 plain。
export type Tone = Mark | "plain";

export function worstLevel(tones: readonly (Tone | undefined)[]): Level | undefined {
  return levels.find((l) => tones.includes(l));
}

// 先后：三级提醒从重到轻，更新、可能改名排在最后(更新另画一个图标，和提醒放在一起时也不盖住提醒)。
const order: readonly Mark[] = [...levels, "update", "rename"];

export function byLevel(a: Mark, b: Mark): number {
  return order.indexOf(a) - order.indexOf(b);
}

// 列表一行的状态只写一条(见 components/ui/badge 的 AlertIcon labeled):严重、注意在前，接着是能做的更新、可能改名，提示最后。
const urgency: readonly Mark[] = ["critical", "warn", "update", "rename", "info"];

export function byUrgency(a: Mark, b: Mark): number {
  return urgency.indexOf(a) - urgency.indexOf(b);
}
