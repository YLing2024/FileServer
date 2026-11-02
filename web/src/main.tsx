import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import { initTheme } from './components/ThemeToggle';
import './styles/tokens.css';
import './styles/global.css';

// 在首次渲染前落主题，避免深色用户看到一帧浅色闪屏。
initTheme();

const container = document.getElementById('root');
if (!container) throw new Error('缺少 #root 挂载点');

createRoot(container).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
