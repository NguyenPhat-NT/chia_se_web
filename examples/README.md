# Website thử GửiWeb

Tải [guiweb-demo.zip](https://github.com/NguyenPhat-NT/chia_se_web/raw/refs/heads/main/examples/guiweb-demo.zip)
để thử upload một website tĩnh nhỏ. Chọn chế độ **ZIP** trong GửiWeb và upload
nguyên file ZIP, không cần giải nén. Dùng slug `demo` hoặc một tên còn trống.

Gói có `index.html` ở gốc và các tài nguyên trong `assets/`, không cần build,
CDN hoặc dịch vụ bên ngoài. Mã nguồn nằm trong thư mục `guiweb-demo/`.

Sau khi mở link website:

1. Kiểm tra giao diện có màu và bố cục: CSS đã tải được.
2. Kiểm tra chỉ báo JavaScript và thử tăng bộ đếm.
3. Lưu một ghi chú, tải lại trang và kiểm tra ghi chú còn nguyên.
4. Mở bằng điện thoại và trình duyệt khác để thử bố cục và quyền xem công khai.

Ghi chú dùng `localStorage`, chỉ lưu trên trình duyệt và origin hiện tại;
không được gửi lên router hay đồng bộ giữa các thiết bị. Nếu trình duyệt chặn
lưu trữ, website vẫn hoạt động và hiển thị thông báo tương ứng.

Cũng có thể giải nén ZIP trên máy tính rồi upload thư mục `guiweb-demo`
(hoặc thư mục chứa trực tiếp `index.html`) để thử chế độ thư mục.

Để thử cập nhật cùng link, sửa tiêu đề trong `guiweb-demo/index.html`, nén lại
các file sao cho `index.html` ở gốc ZIP, rồi chọn cập nhật nội dung website
đã tạo. Website này gồm nhiều file nên dùng ZIP hoặc thư mục.
