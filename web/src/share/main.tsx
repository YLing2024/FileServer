import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { initTheme } from '../components/ThemeToggle';
import { ShareApp } from './ShareApp';
import '../styles/tokens.css';
import '../styles/global.css';

// 公开页也复用同一套深浅令牌；没有用户选择时跟随系统偏好。
initTheme();

const container = document.getElementById('root');
if (!container) throw new Error('缺少 #root 挂载点');

createRoot(container).render(
  <StrictMode>
    <ShareApp />
  </StrictMode>,
);
