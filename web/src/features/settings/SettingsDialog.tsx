/**
 * 设置/关于对话框：版本信息、冷门格式开关说明、会话入口与退出登录。
 *
 * 版本字段来自 `/api/health`（version/commit/go/platform），服务标识来自
 * `/api/info`；冷门格式文案沿用旧前端，避免两套前端说法不一致。
 */
import { Modal } from '../../components/Modal';
import buttonStyles from '../../styles/buttons.module.css';
import styles from './SettingsDialog.module.css';

interface SettingsDialogProps {
  name: string;
  version: string;
  commit: string;
  goVersion: string;
  platform: string;
  ffmpeg: boolean;
  hls: boolean;
  hlsBusy: boolean;
  manage: boolean;
  onToggleHls: () => void;
  onOpenSessions: () => void;
  onLogout: () => void;
  onClose: () => void;
}

/** 键值行；空值显示占位符，避免布局跳动。 */
function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div className={styles.infoRow}>
      <span className={styles.infoLabel}>{label}</span>
      <span className={styles.infoValue}>{value || '—'}</span>
    </div>
  );
}

/** 设置/关于对话框。 */
export function SettingsDialog({
  name,
  version,
  commit,
  goVersion,
  platform,
  ffmpeg,
  hls,
  hlsBusy,
  manage,
  onToggleHls,
  onOpenSessions,
  onLogout,
  onClose,
}: SettingsDialogProps) {
  return (
    <Modal
      title="设置"
      onClose={onClose}
      testId="settings-dialog"
      wide
      footer={
        <button type="button" className={buttonStyles.button} onClick={onClose}>
          关闭
        </button>
      }
    >
      <div className={styles.sections}>
        <section className={styles.section} aria-labelledby="settings-about">
          <h3 id="settings-about" className={styles.sectionTitle}>
            关于
          </h3>
          <div className={styles.infoGrid}>
            <InfoRow label="服务" value={name} />
            <InfoRow label="版本" value={version} />
            <InfoRow label="提交" value={commit} />
            <InfoRow label="Go" value={goVersion} />
            <InfoRow label="平台" value={platform} />
          </div>
        </section>

        <section className={styles.section} aria-labelledby="settings-hls">
          <h3 id="settings-hls" className={styles.sectionTitle}>
            冷门格式支持
          </h3>
          <p className={styles.text}>
            冷门格式支持（MKV/RMVB/HEVC 等在线播放与缩略图，需 ffmpeg）。开启后由服务端实时转为
            H.264 边转边播，并生成视频缩略图；关闭时这些文件只显示图标。
          </p>
          <div className={styles.statusRow}>
            <span className={hls ? styles.statusOn : styles.statusOff}>
              {ffmpeg ? (hls ? '已开启' : '已关闭') : '不可用'}
            </span>
            <button
              type="button"
              className={`${buttonStyles.button} ${buttonStyles.small}`}
              disabled={!ffmpeg || hlsBusy}
              onClick={onToggleHls}
              data-testid="settings-hls-toggle"
            >
              {hlsBusy ? '切换中…' : hls ? '关闭' : '开启'}
            </button>
          </div>
          {!ffmpeg ? <p className={styles.hint}>服务端未找到 ffmpeg，冷门格式暂不可用。</p> : null}
        </section>

        {manage ? (
          <section className={styles.section} aria-labelledby="settings-account">
            <h3 id="settings-account" className={styles.sectionTitle}>
              账户
            </h3>
            <div className={styles.statusRow}>
              <button
                type="button"
                className={`${buttonStyles.button} ${buttonStyles.small}`}
                onClick={onOpenSessions}
                data-testid="settings-sessions"
              >
                已登录设备/会话
              </button>
              <button
                type="button"
                className={`${buttonStyles.button} ${buttonStyles.small} ${buttonStyles.danger}`}
                onClick={onLogout}
                data-testid="settings-logout"
              >
                退出登录
              </button>
            </div>
          </section>
        ) : null}
      </div>
    </Modal>
  );
}
