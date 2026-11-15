// 用 div 当按钮的行(里面还有别的按钮，不能整行用 <button>)按 Enter / 空格时，和点击一样。
// 只认焦点在行本身时的按键，行里的按钮各自处理。
export function pressKeys(onPress: () => void) {
  return (e: { key: string; target: unknown; currentTarget: unknown; preventDefault: () => void }) => {
    if (e.target !== e.currentTarget || (e.key !== "Enter" && e.key !== " ")) return;
    e.preventDefault();
    onPress();
  };
}
