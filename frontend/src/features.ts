// 功能开关的注册表。
//
// 以后加新功能就在这张表里加一行：设置窗口的「功能开关」一栏会自己长出那一行，
// 组件里用 featureOn('xxx') 判断。默认值写在这里，**不要**抄进 settings.json ——
// 抄了的话，以后想把默认值改成开，用户机器上那份旧默认会把它顶回去。
//
// 用户改过的才落盘（Settings.features，键是这里的 id）。
import type { Settings } from './api'

export interface FeatureDef {
  id: string
  /** 设置文件里没写过时按这个算 */
  defaultOn: boolean
}

export const FEATURES: FeatureDef[] = [{ id: 'sessionTabs', defaultOn: true }]

/** 设置文件里改过就以文件为准，没改过用注册表的默认值。 */
export function featureOn(settings: Settings | null | undefined, id: string): boolean {
  const stored = settings?.features?.[id]
  if (stored !== undefined && stored !== null) return stored
  return FEATURES.find((f) => f.id === id)?.defaultOn ?? false
}
