# Go 서버: 시트 스키마 · SQLite · API 계약 (초안)

상위 문서: `2026-11-09-go-server-design.md`. 표시: **[결정]** 사용자 결정, **[판단]** 작성자 판단(검토 필요).

## 1. 공통 규칙
- 모든 쓰기 항목에 `기록자`(이메일), `수정자`, `수정시각`(ISO, 서버 시각)을 둔다. **[결정: 작성자 표시]**
- 항목 ID는 클라이언트가 만든 UUID(기존 지출 방식). 같은 ID 재전송은 멱등(중복 추가 안 함).
- **[결정]** 편집자는 남의 항목도 수정·삭제할 수 있다. 모든 쓰기는 `변경이력`에 기록한다. 삭제는 행을 지우되 이전 값 전체를 `변경이력`에 JSON으로 남긴다(복구 가능).
- **[판단]** 동시 수정 충돌: 수정·삭제 요청은 클라이언트가 본 `수정시각`(`baseUpdatedAt`)을 보낸다. 서버의 현재 값과 다르면 409 `다른 사람이 먼저 수정했습니다`와 최신 값을 돌려준다. 추가(append)는 충돌이 없다.
- 시트에는 RAW 입력으로 쓴다(수식 주입 방지). 날짜·시각·ID 열은 텍스트 서식(기존 유지).

## 2. 시트 탭 (소유자 Google 계정, 원본)
| 탭 | 컬럼 | 비고 |
|---|---|---|
| `설정` | 기존 항목 그대로 | 사람이 직접 편집 |
| 지출(이름은 설정) | 기존 10컬럼(날짜, 시간, 분류, 내용, 금액, 환산, 결제자, 결제수단, ID, 입력통화) + `기록자`, `수정자`, `수정시각` | 기존 시트에 열 추가만 하면 호환 |
| `_동선` | 날짜, 순서, 종류(place/event), 참조ID, 체류분, `수정자`, `수정시각` | 하루 = 여러 행. 날짜 단위로 교체 저장 |
| `_출발` | 날짜, 출발분, 출발지ID, `수정자`, `수정시각` | |
| `_일정장소` | 일정ID, 장소ID, `수정자`, `수정시각` | 캘린더 일정↔장소 연결 |
| `방문기록` | ID, 날짜, 시각, 장소ID, 이름, 위도, 경도, 메모, `기록자`, `수정자`, `수정시각` | 장소ID 또는 이름 중 하나는 필수 |
| `사진` | ID, 날짜, 촬영시각, DriveFileId, 캡션, 방문ID, 지출ID, `기록자`, `수정자`, `수정시각` | 원본은 Drive 앱 폴더 |
| `멤버` | 이메일, 표시이름, 역할(소유자/편집자/보기) | 권한의 원본 |
| `변경이력` | 시각, 이메일, 탭, 항목ID, 동작(add/update/delete), 이전값(JSON), 이후값(JSON) | append-only |

캘린더: 서버가 만든 일정은 비공개 extendedProperties에 `createdBy`, `tripRoute` 표시를 넣는다(기존 `#trip-route` 설명 태그도 읽기 호환 유지).

## 3. SQLite (연동 정보와 재생성 가능한 캐시만)
- `owner_token`(refresh token, 암호화), `sessions`(세션ID, 이메일, 만료)
- `snapshots`(탭, 마지막 확인한 modifiedTime, JSON) — 읽기 캐시. 삭제해도 재읽기로 복구
- `calendar_sync`(syncToken), `photo_map`(Photos mediaItem ↔ DriveFileId)
- `travel_cache`, `hours_cache`, `kml_cache` — 기존 숨김 시트 `_이동시간`, `_영업시간`을 대체
- 동행자 Google 토큰은 저장하지 않는다(상위 문서 §11).

## 4. API (`POST /api/<함수명>`, JSON)
- 요청 `{ "args": [...] }`, 응답 `{ "ok": true, "data": ... }` 또는 `{ "error": "메시지" }`.
- 상태 코드: 401 로그인 필요, 403 권한 없음, 409 충돌(최신 값 포함), 422 입력 오류.
- 인증은 세션 쿠키. 기존의 마지막 인자 `editKey`는 폐지한다.
- 권한: 보기=읽기만, 편집자=쓰기, 소유자=쓰기+멤버·설정 관리.

| 함수 | 인자 → 반환 | 권한 |
|---|---|---|
| `bootstrap` | () → `{config, version, sheet, events, expenses, state, places, visits, photos, me, members}` (`me` = 이메일·이름·역할) | 보기 |
| `refresh` | () → `{state, expenses, events, visits, photos}` | 보기 |
| `diagnose` | () → 점검 목록(설정, My Maps, 캘린더, Places 키, **소유자 토큰 상태**) | 보기 |
| `addExpense` | (e) → 전체 지출 목록 | 편집자 |
| `updateExpense` | (id, e, baseUpdatedAt) → 전체 지출 목록 | 편집자 |
| `deleteExpense` | (id, baseUpdatedAt) → 전체 지출 목록 | 편집자 |
| `saveState` | (key, patch) → 병합된 값. key = routes·starts·startFrom·eventPlace, 기존 부분 병합 의미 유지 | 편집자 |
| `registerRoute` / `unregisterRoute` | (date, items) / (date) → 그날 일정 | 편집자 |
| `getTravelTimes` | (pairs) → 분 단위 값 | 보기 |
| `addVisit` / `updateVisit` / `deleteVisit` | (v) / (id, v, baseUpdatedAt) / (id, baseUpdatedAt) → 전체 방문 목록 | 편집자 |
| `startPhotoPicker` | () → `{sessionId, pickerUri}` | 편집자 |
| `finishPhotoPicker` | (sessionId, {date, visitId?, expenseId?}) → 추가된 사진 | 편집자 |
| `updatePhoto` / `deletePhoto` | (id, patch, baseUpdatedAt) / (id, baseUpdatedAt) | 편집자 |
| `listHistory` | ({탭?, 항목ID?, 이메일?}) → 변경이력 | 소유자 |
| `listMembers` / `setMember` / `removeMember` | 멤버 관리 | 소유자 |
| `GET /api/photo/<id>/thumb`, `/full` | 서버가 Drive에서 읽어 전달(이미지) | 보기 |
| `GET /auth/login`, `/auth/callback`, `POST /auth/logout`, `GET /auth/owner` | 로그인, 소유자 토큰 최초 연결 | - |

메뉴 기능(링크·편집 키·전용 캘린더 만들기)은 소유자 관리 화면으로 옮긴다(`createTripCalendar`는 관리 API로).

## 5. 화면 영향
- `remote.js`: `google.script.run` → `fetch`, 오류 코드별 처리(409는 최신 값으로 갱신 후 안내). `CAN_EDIT` 는 `me.role`로 대체.
- 항목마다 작성자 칩(`기록자`→표시이름), 사람별 필터. 구글 캘린더에서 직접 만든 일정은 "캘린더 직접 입력".

## 6. 미확인
- Sheets 읽기·쓰기 쿼터와 배치 크기, Photos Picker 스코프 검증 요건, Drive 최소 스코프, Routes API 한도.

## 7. 슬롯·후보 모델 (추가, 상위 문서 §12)

`_동선` 의 정의를 아래로 **대체**한다(종류 `place` 는 폐지: 후보 1개짜리 슬롯으로 취급, 읽을 때 변환).

| 탭 | 컬럼 | 비고 |
|---|---|---|
| `_동선` | 날짜, 순서, 종류(slot/event), 슬롯ID 또는 일정ID, 라벨, 목표시각(선택), 체류분, 확정장소ID(선택), `수정자`, `수정시각` | 하루 = 여러 행. 후보 0개 슬롯 허용 |
| `_후보` | 슬롯ID, 장소ID(또는 이름), 순서, `추가자`, `수정시각` | 후보 상태는 저장하지 않는다 |
| `방문기록`(확장) | 기존 컬럼 + `슬롯ID`, `결과`(방문/닫힘/건너뜀), `사유` | 후보 상태 = 슬롯·장소별 최신 기록 |

신규 API (권한: 편집자): `addCandidate(slotId, 장소ID|이름)`, `removeCandidate(slotId, 장소ID, baseUpdatedAt)`, `confirmSlot(slotId, 장소ID, baseUpdatedAt)`(확정 장소 기록), `addVisit(v)`(v 에 슬롯ID·결과 포함, `결과=닫힘` 이면 서버가 슬롯의 확정을 해제), `skipSlot(slotId, 사유)`.
`saveState` 의 `routes` 항목은 `{cands:[…], pick, stayMin, label}` 형태를 받고 기존 `{id, stayMin}` 도 계속 허용한다.
시간 계산(route.js): 슬롯의 유효 장소 = 확정 장소 → 임시 선택(열려 있는 후보 중 첫 곳) → 없으면 위치 없는 슬롯(이동 0).
