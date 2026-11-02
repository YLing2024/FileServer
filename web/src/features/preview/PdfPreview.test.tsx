import { act, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { PdfPreview } from './PdfPreview';

function setPdfViewer(enabled: boolean): void {
  Object.defineProperty(navigator, 'pdfViewerEnabled', {
    configurable: true,
    value: enabled,
  });
}

afterEach(() => {
  Reflect.deleteProperty(navigator, 'pdfViewerEnabled');
});

describe('PdfPreview', () => {
  it('浏览器支持内嵌 PDF 时渲染 iframe 并常驻下载入口', async () => {
    setPdfViewer(true);
    render(<PdfPreview path="a.pdf" name="a.pdf" onToast={vi.fn()} />);
    await act(async () => {
      await Promise.resolve();
    });
    expect(await screen.findByTestId('pdf-frame')).toBeTruthy();
    expect(screen.getByRole('button', { name: '下载 PDF' })).toBeTruthy();
  });

  it('未启用 PDF 查看器时显示下载兜底，不渲染 iframe', async () => {
    setPdfViewer(false);
    render(<PdfPreview path="a.pdf" name="a.pdf" onToast={vi.fn()} />);
    await act(async () => {
      await Promise.resolve();
    });
    expect(screen.getByRole('alert').textContent).toContain('未启用内置 PDF 查看器');
    expect(screen.queryByTestId('pdf-frame')).toBeNull();
    expect(screen.getByRole('button', { name: '下载 PDF' })).toBeTruthy();
  });
});
