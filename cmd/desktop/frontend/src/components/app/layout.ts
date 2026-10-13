// 三栏布局的公共样式：主体区域(左栏、中间底板、右栏横排)与中间的半透明底板，主界面与设置页共用。
export const bodyClass = "relative flex min-h-0 flex-1 gap-2 px-2.5 pb-2.5";
export const centerClass =
  "flex min-w-0 flex-1 flex-col rounded-tile border border-hairline bg-glass-tile shadow-tile backdrop-blur-[20px]";
// 左右栏填满外面的 ResizablePane(宽度由它管),自己滚动。
export const leftClass = "a-left flex min-h-0 flex-1 flex-col overflow-y-auto px-0.5 py-1";
export const rightClass = "a-right min-h-0 flex-1 overflow-y-auto px-0.5";

// 顶栏：整条是窗口拖动区(--wails-draggable 会继承),能点的元素必须显式 no-drag,
// 否则 mousedown 被当成拖窗口，输入框拿不到焦点。titlebar 类名是测试钩子。
export const titlebarClass =
  "titlebar grid h-[50px] shrink-0 grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-center gap-3 px-3.5 [&_:is(button,input,label,select,[data-no-drag])]:[--wails-draggable:no-drag]";
