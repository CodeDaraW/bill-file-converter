import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import * as pdfjs from 'pdfjs-dist';
import { App as AntdApp } from 'antd';
import App from './App.tsx';
import './index.css';

pdfjs.GlobalWorkerOptions.workerSrc = `//cdnjs.cloudflare.com/ajax/libs/pdf.js/${pdfjs.version}/pdf.worker.min.mjs`;

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <AntdApp>
      <App />
    </AntdApp>
  </StrictMode>,
);
