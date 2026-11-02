import { act, fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Lightbox } from './Lightbox';

async function renderLightbox(overrides: Partial<Parameters<typeof Lightbox>[0]> = {}) {
  const props = {
    path: 'photos/a.jpg',
    paths: ['photos/a.jpg', 'photos/b.jpg'],
    onClose: vi.fn(),
    onChange: vi.fn(),
    onToast: vi.fn(),
    ...overrides,
  };
  render(<Lightbox {...props} />);
  await act(async () => {
    await Promise.resolve();
  });
  return props;
}

describe('Lightbox', () => {
  it('展示文件名与计数，键盘左右切换图片', async () => {
    const props = await renderLightbox();
    expect(screen.getByRole('dialog')).toBeTruthy();
    expect(screen.getByText('a.jpg')).toBeTruthy();
    expect(screen.getByText('1 / 2')).toBeTruthy();

    fireEvent.keyDown(document, { key: 'ArrowRight' });
    expect(props.onChange).toHaveBeenCalledWith('photos/b.jpg');

    fireEvent.keyDown(document, { key: 'ArrowLeft' });
    expect(props.onChange).toHaveBeenLastCalledWith('photos/b.jpg');
  });

  it('滚轮与 +/- 键缩放，百分比同步', async () => {
    await renderLightbox();
    const stage = screen.getByTestId('lightbox-stage');
    fireEvent.wheel(stage, { deltaY: -100 });
    expect(screen.getByText('120%')).toBeTruthy();

    fireEvent.keyDown(document, { key: '+' });
    expect(screen.getByText('180%')).toBeTruthy();

    fireEvent.keyDown(document, { key: '-' });
    expect(screen.getByText('120%')).toBeTruthy();
  });

  it('Esc 关闭，切图按钮只触发回调', async () => {
    const props = await renderLightbox();
    fireEvent.click(screen.getByRole('button', { name: '下一张' }));
    expect(props.onChange).toHaveBeenCalledWith('photos/b.jpg');
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(props.onClose).toHaveBeenCalled();
  });
});
