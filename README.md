# eduroam-sp v2.2.2.1

โปรแกรมวิเคราะห์ข้อมูล Access-Accept สำหรับ Service Provider ของระบบ eduroam

## คำอธิบาย

โปรแกรม eduroam-sp เป็นเครื่องมือที่ใช้ในการวิเคราะห์เหตุการณ์ Access-Accept สำหรับผู้ให้บริการที่ระบุ (Service Provider) โดยใช้ความสามารถในการรวมข้อมูล (aggregation) ของ Quickwit search engine โปรแกรมจะรวบรวมข้อมูลตลอดช่วงเวลาที่กำหนด ประมวลผลตามโดเมน (realms) และผู้ใช้ (users) แล้วส่งออกข้อมูลที่รวบรวมไว้ไปยังไฟล์ JSON หรือ CSV

## คุณสมบัติหลัก

- การรวมข้อมูลประสิทธิภาพสูงใช้การ aggregation queries ของ Quickwit
- การประมวลผลแบบขนานที่ปรับแต่งได้ด้วย worker pools
- รองรับการระบุช่วงเวลาที่ยืดหยุ่น: จำนวนวัน, จำนวนปี, ปีที่เฉพาะเจาะจง, หรือวันที่เฉพาะเจาะจง
- การรายงานความคืบหน้าแบบเรียลไทม์พร้อมจำนวนฮิตที่แม่นยำและการคำนวณเวลาที่คาดว่าจะเสร็จ (ETA)
- รองรับหลายรูปแบบการส่งออก (JSON, CSV)
- การจัดการข้อผิดพลาดที่ปรับปรุงและกลไกการลองใหม่
- การบันทึกที่ปรับแต่งได้ (configurable logging) พร้อมการบันทึกลงไฟล์
- การประมวลผลที่ประหยัดหน่วยความจำสำหรับชุดข้อมูลขนาดใหญ่

## การติดตั้ง

### ความต้องการของระบบ

- Go 1.19 หรือสูงกว่า
- การเข้าถึง Quickwit server ที่มีข้อมูล eduroam
- สิทธิ์ในการเขียนไฟล์ไปยังไดเรกทอรีเอาต์พุต

### การติดตั้ง

1. โคลนหรือดาวน์โหลดรีโพสิทอรีนี้
2. ติดตั้งแพคเกจที่จำเป็นโดยใช้คำสั่ง:

```bash
go get golang.org/x/time/rate
```

3. คอมไพล์โปรแกรม:

```bash
go build -o eduroam-sp
```

## การตั้งค่า

โปรแกรมใช้ไฟล์การตั้งค่า `qw-auth.properties` เพื่อจัดเก็บข้อมูลการยืนยันตัวตนสำหรับ Quickwit API สร้างไฟล์นี้ด้วยเนื้อหาต่อไปนี้:

```properties
QW_USER=your_username
QW_PASS=your_password
QW_URL=http://your_quickwit_server:7280
```

นอกจากนี้ยังสามารถกำหนดค่าโปรแกรมผ่านตัวแปรสภาพแวดล้อม:

- `EDUROAM_NUM_WORKERS`: จำนวน worker goroutine (ค่าเริ่มต้น 10)
- `EDUROAM_MAX_RETRIES`: จำนวนครั้งสูงสุดที่จะลองใหม่สำหรับคำขอที่ล้มเหลว (ค่าเริ่มต้น 3)
- `EDUROAM_BATCH_SIZE`: จำนวนรายการบันทึกที่จะส่งในแต่ละ batch (ค่าเริ่มต้น 10000)
- `EDUROAM_REQUEST_TIMEOUT`: ระยะเวลา timeout ในหน่วยวินาทีสำหรับคำขอ API (ค่าเริ่มต้น 60)
- `EDUROAM_OUTPUT_FORMAT`: รูปแบบการส่งออก: "json" หรือ "csv" (ค่าเริ่มต้น "json")
- `EDUROAM_LOG_LEVEL`: ระดับการบันทึก: "error", "warn", "info", หรือ "debug" (ค่าเริ่มต้น "info")
- `EDUROAM_LOG_FILE`: เส้นทางไฟล์บันทึก (ค่าเริ่มต้น "logs/eduroam-sp.log")
- `EDUROAM_RATE_LIMIT`: อัตราการจำกัดคำขอต่อวินาที (ค่าเริ่มต้น 50)

## การใช้งาน

รูปแบบคำสั่งพื้นฐาน:

```bash
./eduroam-sp [flags] <service_provider> [days|Ny|yxxxx|DD-MM-YYYY]
```

### พารามิเตอร์:

- `<service_provider>`: โดเมนของผู้ให้บริการที่ต้องการค้นหา เช่น 'ku.ac.th', 'etlr1'
- `[days]`: (ไม่บังคับ) จำนวนวัน (1-3650) ที่จะย้อนกลับจากวันที่ปัจจุบัน
- `[Ny]`: (ไม่บังคับ) จำนวนปี (1y-10y) ที่จะย้อนกลับจากวันที่ปัจจุบัน
- `[yxxxx]`: (ไม่บังคับ) ปีที่เฉพาะเจาะจง เช่น 'y2024'
- `[DD-MM-YYYY]`: (ไม่บังคับ) วันที่เฉพาะเจาะจงที่จะประมวลผลข้อมูล

### Flags:

- `-config string`: เส้นทางไปยังไฟล์การตั้งค่า (ค่าเริ่มต้น "qw-auth.properties")
- `-format string`: รูปแบบการส่งออก: "json" หรือ "csv"
- `-log-level string`: ระดับการบันทึก: "error", "warn", "info", หรือ "debug"
- `-log-file string`: เส้นทางไปยังไฟล์บันทึก
- `-workers int`: จำนวน worker goroutine

### ตัวอย่าง:

ค้นหาข้อมูล Access-Accept สำหรับ "ku.ac.th" ในช่วง 30 วันที่ผ่านมา:

```bash
./eduroam-sp ku.ac.th 30
```

ค้นหาข้อมูลสำหรับ "etlr1" ในปี 2024:

```bash
./eduroam-sp etlr1 y2024
```

ค้นหาข้อมูลสำหรับ "mahidol.ac.th" ในวันที่เฉพาะเจาะจง (12 มกราคม 2025):

```bash
./eduroam-sp mahidol.ac.th 12-01-2025
```

ค้นหาข้อมูลสำหรับ "chula.ac.th" ในช่วง 2 ปีที่ผ่านมา และส่งออกเป็น CSV:

```bash
./eduroam-sp -format=csv chula.ac.th 2y
```

ค้นหาข้อมูลด้วยการตั้งค่าที่กำหนดเอง:

```bash
./eduroam-sp -config=custom-config.properties -log-level=debug -workers=16 ku.ac.th 30
```

## รูปแบบเอาต์พุต

### JSON

ไฟล์ JSON ที่ส่งออกจะมีโครงสร้างต่อไปนี้:

```json
{
  "query_info": {
    "service_provider": "eduroam.ku.ac.th",
    "days": 30,
    "start_date": "2025-01-27 00:00:00",
    "end_date": "2025-02-26 23:59:59",
    "total_hits": 12345
  },
  "description": "Aggregated Access-Accept events for the specified service provider and time range.",
  "summary": {
    "total_users": 100,
    "total_realms": 15,
    "total_active_days": 450
  },
  "realm_stats": [
    {
      "realm": "eduroam.chula.ac.th",
      "user_count": 25,
      "users": ["user1@chula.ac.th", "user2@chula.ac.th", ...],
      "first_seen": "2025-01-27",
      "last_seen": "2025-02-26"
    },
    ...
  ],
  "user_stats": [
    {
      "username": "user1@chula.ac.th",
      "active_days": 20,
      "first_seen": "2025-01-27",
      "last_seen": "2025-02-26"
    },
    ...
  ],
  "trend_analysis": {
    "daily_activity": [
      {
        "date": "2025-01-27",
        "count": 15
      },
      ...
    ]
  }
}
```

### CSV

เมื่อเลือก format เป็น CSV โปรแกรมจะสร้างไฟล์ CSV สองไฟล์:

1. `{timestamp}-users.csv` - มีข้อมูลผู้ใช้:
   - Username
   - Active Days
   - First Seen
   - Last Seen

2. `{timestamp}-realms.csv` - มีข้อมูล realm:
   - Realm
   - User Count
   - First Seen
   - Last Seen

## การขัดข้องและวิธีแก้ไข

### ปัญหาทั่วไป

1. **การเชื่อมต่อกับ Quickwit ล้มเหลว**
   - ตรวจสอบว่า URL, username และ password ในไฟล์การตั้งค่าถูกต้อง
   - ตรวจสอบว่า Quickwit server กำลังทำงานและสามารถเข้าถึงได้

2. **หน่วยความจำไม่เพียงพอสำหรับชุดข้อมูลขนาดใหญ่**
   - ลดขนาด batch ด้วยตัวแปรสภาพแวดล้อม `EDUROAM_BATCH_SIZE`
   - ลดจำนวน worker ด้วย flag `-workers`

3. **การประมวลผลช้า**
   - เพิ่มจำนวน worker ด้วย flag `-workers`
   - เพิ่มอัตราการจำกัดด้วยตัวแปรสภาพแวดล้อม `EDUROAM_RATE_LIMIT`

## การพัฒนาต่อ

### การเพิ่มประสิทธิภาพในอนาคต

- การรองรับฐานข้อมูลสำหรับบันทึกข้อมูลประวัติ
- การรองรับการส่งออกในรูปแบบ Excel (.xlsx)
- การเพิ่มคุณสมบัติการแสดงผลข้อมูลเชิงโต้ตอบ
- การเพิ่มการรองรับการเปรียบเทียบช่วงเวลา

## ผู้จัดทำ

- [P.Itarun] - อีเมล/ข้อมูลติดต่อ

## ประวัติการเปลี่ยนแปลง

### v2.2.2.1 (26 กุมภาพันธ์ 2025)
- ปรับปรุงการจัดการข้อผิดพลาดและกลไกการกู้คืน
- เพิ่มการตั้งค่า timeout สำหรับคำขอ API
- เพิ่มการใช้ exponential backoff สำหรับการลองใหม่
- ปรับปรุงการจัดการหน่วยความจำสำหรับชุดข้อมูลขนาดใหญ่
- เพิ่มการบันทึกลงไฟล์พร้อมระดับการบันทึกที่ปรับแต่งได้
- ย้ายการตั้งค่าไปยังตัวแปรสภาพแวดล้อมและไฟล์การตั้งค่า
- เพิ่มตัวเลือกการส่งออกเป็น CSV
- เพิ่มการใช้ caching สำหรับคำขอที่ทำบ่อย
- เพิ่มการวิเคราะห์แนวโน้มเปรียบเทียบกับช่วงเวลาก่อนหน้า
- ปรับปรุงความปลอดภัยสำหรับการจัดการข้อมูลรับรอง
- ปรับปรุงเอกสารประกอบและโครงสร้างโค้ด
- เพิ่มการคำนวณ ETA สำหรับความคืบหน้า

### v2.2.1 (12 กุมภาพันธ์ 2025)
- เพิ่มการรองรับการกำหนดปีเฉพาะเจาะจง (เช่น 'y2024')
- ปรับปรุงการจัดการข้อมูลรายวัน
- แก้ไขปัญหาเกี่ยวกับการแสดงผลข้อมูล
- ปรับปรุงประสิทธิภาพการค้นหา

### v2.2.0 (1 กุมภาพันธ์ 2025)
- เพิ่มการรองรับการระบุช่วงเวลาเป็นปี (1y-10y)
- เพิ่มการรองรับช่วงเวลาสูงสุดถึง 10 ปี (3650 วัน)
- เพิ่มการใช้ aggregation queries ของ Quickwit
- ปรับปรุงประสิทธิภาพด้วยการจัดการ worker pool ที่ดีขึ้น
- ลดความซับซ้อนของโค้ด
- แก้ไขการรวมข้อมูลวันที่โดยใช้ fixed intervals
- ปรับปรุงการรายงานความคืบหน้าด้วยการนับฮิตที่แม่นยำ
- ลดการใช้หน่วยความจำในการประมวลผลข้อมูล

## ข้อตกลงการใช้งาน

โปรแกรมนี้พัฒนาขึ้นสำหรับใช้ภายในองค์กรเครือข่าย eduroam ประเทศไทย การนำไปใช้หรือเผยแพร่ต้องได้รับอนุญาตจากผู้พัฒนาก่อน