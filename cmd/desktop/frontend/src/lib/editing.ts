// 同一时间只有一个对象在编辑(抽屉里的备注):进入编辑记下它的 id,结束时只放掉自己那一次。
// 旧的异步保存晚于切换完成时，不会关掉后来开始的另一处编辑。
export function editingAfter<T>(current: T | null, id: T, on: boolean): T | null {
  if (on) return id;
  return current === id ? null : current;
}
