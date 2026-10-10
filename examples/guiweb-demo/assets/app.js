'use strict';

(() => {
  const $ = id => document.getElementById(id);
  const key = 'guiweb-demo-note-v1';
  let count = 0;
  let storageUnavailable = false;

  $('hostname').textContent = location.hostname || 'Tệp đang mở trên máy của bạn';
  $('js-status').textContent = 'Đã tải, sẵn sàng tương tác';
  $('js-icon').textContent = '✓';
  $('js-icon').classList.remove('waiting');

  const cssLoaded = getComputedStyle(document.documentElement).getPropertyValue('--demo-css-ready').trim() === '1';
  $('css-status').textContent = cssLoaded ? 'Màu sắc đã sẵn sàng' : 'Chưa tải được /assets/style.css';
  $('css-icon').textContent = cssLoaded ? '✓' : '!';
  $('css-icon').classList.remove('waiting');
  if (!cssLoaded) $('css-icon').classList.add('failed');

  for (const id of ['counter-button', 'reset-button', 'note-text', 'save-note-button', 'clear-note-button']) $(id).disabled = false;

  $('counter-message').textContent = 'Bấm thử nhé. Bạn có thể gửi bao nhiêu lời chào cũng được.';
  $('counter-button').addEventListener('click', () => {
    count += 1;
    $('counter-value').textContent = new Intl.NumberFormat('vi-VN').format(count);
    $('counter-message').textContent = count === 1 ? 'Lời chào đầu tiên đã đến nơi!' : `Đã nhận ${new Intl.NumberFormat('vi-VN').format(count)} lời chào. Website của bạn đang hoạt động.`;
  });
  $('reset-button').addEventListener('click', () => {
    count = 0;
    $('counter-value').textContent = '0';
    $('counter-message').textContent = 'Đã đặt lại bộ đếm. Bắt đầu một lời chào mới nhé.';
  });

  function updateLength() {
    $('note-count').textContent = String($('note-text').value.length);
  }

  function noteMessage(message, kind = '') {
    $('note-message').textContent = message;
    $('note-message').className = `small-note${kind ? ' ' + kind : ''}`;
  }

  function storageFallback() {
    storageUnavailable = true;
    $('storage-state').textContent = 'Chỉ giữ trong lần mở này';
    noteMessage('Trình duyệt đang không cho phép lưu. Bạn vẫn có thể viết ghi chú, nhưng nội dung sẽ mất khi tải lại trang.', 'warning');
  }

  try {
    const saved = localStorage.getItem(key);
    if (saved !== null) {
      $('note-text').value = saved.slice(0, 1000);
      $('storage-state').textContent = 'Đã khôi phục ghi chú';
      noteMessage('Ghi chú đã lưu của bạn đã trở lại. Chỉ bạn trên trình duyệt này nhìn thấy nó.', 'success');
    } else {
      $('storage-state').textContent = 'Chưa lưu ghi chú';
    }
  } catch {
    storageFallback();
  }
  updateLength();

  $('note-text').addEventListener('input', () => {
    updateLength();
    if (storageUnavailable) return;
    $('storage-state').textContent = 'Có thay đổi chưa lưu';
    noteMessage('Bấm “Lưu ghi chú” để giữ lại nội dung vừa viết.');
  });

  $('note-form').addEventListener('submit', event => {
    event.preventDefault();
    const text = $('note-text').value;
    if (text.length > 1000) {
      noteMessage('Ghi chú dài quá 1000 ký tự. Hãy rút gọn trước khi lưu.', 'warning');
      return;
    }
    try {
      localStorage.setItem(key, text);
      storageUnavailable = false;
      $('storage-state').textContent = 'Đã lưu trên trình duyệt';
      noteMessage('Đã lưu! Thử tải lại trang, ghi chú sẽ được giữ nguyên.', 'success');
    } catch {
      storageFallback();
    }
  });

  $('clear-note-button').addEventListener('click', () => {
    $('note-text').value = '';
    updateLength();
    try {
      localStorage.removeItem(key);
      storageUnavailable = false;
      $('storage-state').textContent = 'Chưa lưu ghi chú';
      noteMessage('Đã xóa ghi chú ở địa chỉ này.', 'success');
    } catch {
      storageFallback();
      noteMessage('Đã xóa nội dung đang hiển thị. Trình duyệt không cho phép xóa dữ liệu lưu trước đó.', 'warning');
    }
    $('note-text').focus();
  });
})();
