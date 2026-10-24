// 对已有的集合再做一次入库预览："看来源里的其他技能"与来源里的新技能("去入库" / "不感兴趣")。
import { LibraryService } from "@bindings/cmd/desktop";
import type { CollectionID } from "@bindings/internal/library";
import { messages } from "@/lib/i18n";
import { ui } from "@/lib/ui";
import { FetchReview } from "./fetch-review";

// previewCollection 对库里已有的集合再做一次入库预览，看作者又发布了哪些技能;已入库的候选标出来、不能再选。
export async function previewCollection(collectionID: CollectionID) {
  const preview = await ui.run(() => LibraryService.PreviewFetch({ collection: collectionID }), { reload: false });
  if (!preview) return;
  await ui.open((close) => <FetchReview preview={preview} onClose={() => close()} onBack={() => close()} />);
}


// previewNewSkills 打开这个集合来源的入库预览，新出现的技能默认勾上。
export async function previewNewSkills(collectionID: CollectionID) {
  const preview = await ui.run(() => LibraryService.PreviewFetch({ collection: collectionID }), { reload: false });
  if (!preview) return;
  await ui.open((close) => <FetchReview preview={preview} onlyNew onClose={() => close()} onBack={() => close()} />);
}

// notInterested 是对新技能点"不感兴趣":集合上不再提，入库预览里仍能找到。
export async function notInterested(collectionID: CollectionID, names: string[]): Promise<boolean> {
  const done = await ui.run(() => LibraryService.MarkNotInterested(collectionID, names).then(() => true), {
    success: messages().update.notInterested,
  });
  return !!done;
}
