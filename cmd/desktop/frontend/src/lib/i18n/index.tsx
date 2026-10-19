// 界面语言：zh.tsx 是文案的唯一来源，en.tsx 照它的结构逐条翻译(漏翻、参数不对由 tsc 报出)。
// 组件里用 useT 取当前语言的文案，切换语言时随之重渲染;组件外的函数(model.ts 等)用 messages 读模块级的当前语言。
// 实际用哪种语言由 Go 端决定(AppService.Language:设置里选的，或跟随系统时按系统的首选语言)。
import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";
import { zh } from "./zh";
import { en } from "./en";

export type Language = "zh" | "en";
export type Messages = typeof zh;

const catalogs: Record<Language, Messages> = { zh, en };

let current: Language = "zh";

function apply(language: Language) {
  current = language;
  document.documentElement.lang = language === "zh" ? "zh-CN" : "en";
}

// toLanguage 把 Go 端给出的语言(AppService.Language)转成界面语言;认不出的按英文，和 Go 端认不出系统语言时一样。
export function toLanguage(value: string): Language {
  return value === "zh" ? "zh" : "en";
}

// messages 是当前语言的文案，给组件外的函数用;组件里用 useT。
export function messages(): Messages {
  return catalogs[current];
}

const I18nContext = createContext<{ language: Language; setLanguage: (language: Language) => void } | null>(null);

export function I18nProvider({ initial, children }: { initial: Language; children: ReactNode }) {
  const [lang, setLang] = useState(() => {
    apply(initial);
    return initial;
  });
  // 先改模块级的当前语言再重渲染，组件外的函数在这次渲染里就读到新语言。
  const setLanguage = useCallback((next: Language) => {
    apply(next);
    setLang(next);
  }, []);
  const value = useMemo(() => ({ language: lang, setLanguage }), [lang, setLanguage]);
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useLanguage() {
  const ctx = useContext(I18nContext);
  if (!ctx) throw new Error("useLanguage outside I18nProvider");
  return ctx;
}

export function useT(): Messages {
  return catalogs[useLanguage().language];
}
