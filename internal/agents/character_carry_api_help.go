package agents

// Fixed API help, never a projection of an arbiter's private conflict text.
// Only the explicitly selected new producer may append this to the owner's
// system prompt; that producer must hash these exact bytes.
const characterCarryAPIHelpV1 = `
本人携带任务的接口说明：当前单终点carry接口支持一项任务携带多件物品。如果你本来就决定将本人已知且有持有权限的物品同程带往同一终点，可在一项self_tasks中声明kind="carry"，以resource_ids列出所有拟携物品；不要仅因物品不同把同一次携带拆成多项需要同时完成的carry。此说明不要求你改变目的地、物品或原有意图。
历史not_started的carry经历不是work累计进度，不锁定后续carry的task_id。你若自主采用一项同程多物件任务，可给本次任务新的唯一task_id并引用本人已有依据；旧经历仍保留为未执行，不能删改，也不能据此更改已有work任务的动作、目标或计量单位。必须由你亲自提交当前任务，Arbiter不能替你合并任务、改写非空原意图或伪造新提案。
这些只是提案的表达能力，不是移动已成功、物品已到场、权限已取得或时间已消耗的证明。仍须满足真实路径、位置、持有权限和时段；尚未执行不能当完成，不能为了回执形状编造阻断、搬运或耗时。`
