# 공개 배포 준비 — LLD · Evaluation Plan (v2.0.0)

범위: 분석 보고서의 A1–A6, B1–B8, C1–C6. 결정(사람 게이트, 2026-11-06): 편집 키 = **선택 사항**, 지도 타일 = **설정에서 변경, 기본 OSM**.

## 1. 공통 계약

### 1.1 쓰기 권한 (A4)
- 스크립트 속성 `EDIT_KEY` (32자 hex). 없으면 지금처럼 링크를 가진 사람 모두 편집할 수 있다.
- 쓰기 서버 함수는 **마지막 인자로 `key`** 를 받는다: `addExpense(e,key)`, `updateExpense(id,e,key)`, `deleteExpense(id,key)`, `saveState(k,patch,key)`, `registerRoute(d,items,key)`, `unregisterRoute(d,key)`.
  `requireEdit_(key)`: `EDIT_KEY`가 있고 `key !== EDIT_KEY` → `보기 전용 링크입니다`.
- `doGet(e)`: `e.parameter.k`가 키와 같을 때만 그 키를 페이지에 넣는다 → `window.EDIT_ACCESS = {required, key}`. 이 값은 build.py가 Apps Script용 index.html에만 넣는다.
- 클라이언트는 `callWrite(fn, ...args)` 한 곳에서만 키를 붙인다. `canEdit = !required || key`.
- 메뉴 함수(`showEditLinks`, `rotateEditKey`, `createTripCalendar`)는 **먼저 `SpreadsheetApp.getUi()`** 를 호출한다. 웹에서 호출하면 여기서 예외가 나므로 아무것도 바뀌지 않는다.

### 1.2 공유 상태 병합 (A1)
- `saveState(key, patch, editKey)`: `patch = {subkey: value | null}` (null = 삭제). 서버가 잠금 안에서 현재 값과 합치고, **합친 결과**를 `STATE_SHAPES`로 검사한 뒤 저장하고 반환한다.
- `routes`는 날짜별 속성 `state.routes.<date>`에 저장한다. 레거시 `state.routes`도 읽고, 쓰기 시 이전(migrate)한 뒤 삭제한다.
- 클라이언트는 키마다 `synced[key]`(마지막 서버 값)와 비교해 바뀐 subkey만 보낸다.
- `refresh()` → `{state, expenses, events}`. 탭이 다시 보일 때(visibilitychange)와 3분마다 호출한다. 저장 대기 중인 키는 덮어쓰지 않는다.

### 1.3 편집 불가 UI 상태 (A4 · C1)
- `isEditable() = canEdit && !isStale`. `body.readonly` CSS로 편집 컨트롤을 숨기고, `handleButton`·change·submit 맨 앞에서 막는다.
- 지난번 캐시로 먼저 그린 상태(`isStale`)는 새 부팅 응답이 오면 풀린다. 설정 미완성 응답이면 바로 풀린다. 부팅이 실패하면 3초 뒤 두 번 다시 시도하고, 그래도 실패하면 캐시 화면을 읽기 전용으로 두고 `bootFailed` 안내와 오류 알림을 띄운다.

### 1.4 동기화 불변식 (검증에서 나온 H1·M4)
- `synced[key]`는 "이 페이지가 서버와 합의한 값"이다. 저장이 성공했을 때 대기 중인 로컬 수정이 있으면 **보낸 패치만** 반영하고(`applyPatch`), 없으면 서버가 합친 값을 그대로 쓰고 화면에도 반영한다.
  이 규칙이 없으면 동행자의 날짜가 `synced`에만 있고 `state`에는 없어서 다음 diff에서 `null`(삭제)로 보내진다.
- 서버가 확인하지 않은 키는 localStorage `trip.dirty`에 남긴다. 다시 열면 그 키는 로컬 값을 유지하고 서버와의 차이만 다시 보낸다. `refresh()`도 이 키는 덮어쓰지 않는다.

### 1.5 공개 함수 남용 방지
- 날짜별 공유 상태(routes/starts/startFrom)는 여행 기간 안의 날짜만 받는다 (속성 수 상한).
- `getTravelTimes`는 보기 전용 사용자에게도 필요하므로 키를 요구하지 않는다. 대신 My Maps 장소 범위(±0.5°) 밖의 좌표는 조회하지 않는다.
- `diagnose()`는 60초 캐시하고 캘린더 이름을 노출하지 않는다. `setup()`은 `getUi()`를 먼저 호출한다. 오래된 `quota.directions.*` 속성은 지운다.

## 2. 항목별 LLD

| ID | 인터페이스 / 동작 | 예외 |
|---|---|---|
| A2 | 설정 `현지 통화 소수 자릿수`(0–2, 기본 0). 서버: 소수 자릿수 초과 → 오류. 입력창 `inputmode=decimal` | `금액은 … 소수 N자리까지` |
| A3 | 설정 `캘린더 ID`(선택). `calendar_()` = ID 있으면 `getCalendarById`, 없으면 기본 캘린더. 메뉴 `여행 전용 캘린더 만들기` → 새 캘린더를 만들고 ID를 설정 탭에 기록 | `캘린더 ID를 찾을 수 없습니다` |
| A5 | `VERSION` 상수 = 저장소 `VERSION` 파일. `latestVersion_()`는 GitHub raw를 UrlFetch로 읽고 6시간 캐시. 실패 시 null. 다르면 띠 표시 | 조회 실패는 조용히 무시하고 로그만 남김 |
| A6 | 일정 탭 하단 문구를 실제 동작(장소 연결 = 공유 상태)에 맞게 수정 | |
| B1 | 설정 `이동수단` 자동차/대중교통/도보. 이동시간 키 끝에 `!t`/`!w`를 붙여 캐시를 분리. Directions Mode, 길찾기 링크 `travelmode`, 예비 계산 속도 | 대중교통 경로가 없으면 null(직선거리 추정) |
| B2 | `Settle.balances(expenses,payers)` → `[{payer,paid,share,balance}]`, `Settle.transfers(bal)` → `[{from,to,amount}]` (탐욕, 최소 건수 근사) | 결제자 목록에 없는 행은 무시 |
| B3 | 날짜: 시작일 365일 전 ~ 종료일. 시작일 이전은 "여행 전"으로 묶음. `currency: 'local'\|'home'`. home이면 환산액을 그대로 쓰고 현지액 = 환산액 ÷ 환율. 10번째 열 `입력 통화` | |
| B4 | `kindOf`: `Flight to X` / `항공편: X` 형식도 인식(X가 목적지 키워드면 도착편, 아니면 출국편). 종일 + 여러 날 + 제목 `/^stay at\|숙박\|호텔\|hotel/i` → 체크인 15:00 / 체크아웃 11:00 숙박 일정 | **Gmail 실제 제목 형식은 미확인** |
| B5 | 클라이언트가 UUID를 만들고, 실패 시 localStorage `trip.outbox`에 쌓는다. `online` 이벤트와 부팅 시 다시 보낸다. 서버는 같은 ID가 있으면 추가하지 않고 목록만 반환(멱등) | |
| B6 | `updateExpense(id, e, key)` — 검증은 add와 같고, 입력 시각은 유지 | `이미 삭제된 지출입니다` |
| B7 | 설정 탭의 환율 칸을 숫자 서식으로 바꿔 `=GOOGLEFINANCE("CURRENCY:JPYKRW")`가 계산되게 함 (setup_ · make_template 둘 다) | `#N/A` → 기존 환율 검증 오류 |
| B8 | `Route.nearestOrder(start, stops)` → 인덱스 배열. 고정 일정의 자리는 유지하고 사용자 장소만 재배치 | |
| C1 | localStorage `trip.boot.<title>|<start>`에 부팅 응답을 저장. 다음에 열 때 먼저 그리고 편집은 잠금 → 새 응답이 오면 교체 | |
| C2 | `#toast` 오류 영역(닫기 버튼 전까지 유지). 상태 띠와 분리 | |
| C3 | 설정 미완성 화면 + "점검" 버튼 → `diagnose()` `[{label, ok, detail}]`. README에 시나리오 캡처·GIF | |
| C4 | `todayIn(tz)` = `Intl.DateTimeFormat('en-CA',{timeZone})` | |
| C5 | 설정 `지도 타일 주소`(https, `{z}{x}{y}` 포함). 비우면 OSM. README에 정책과 대안 안내 | 형식이 틀리면 invalid |
| C6 | hours.js 주석 정리. 일반 핀에 분류 첫 글자 표시(색만으로 구분하지 않음) | |

## 3. Evaluation Plan (KPI)

| 모듈 | KPI | 목표 |
|---|---|---|
| 서버 검증 (server.test.mjs) | 신규 설정·키·병합·금액·날짜 규칙 테스트 | 전부 통과 |
| 순수 모듈 (route/schedule/hours/settle) | 줄 커버리지 (`node --test --experimental-test-coverage`) | ≥ 80% |
| 정산 | 합계 보존: Σbalance = 0, transfers 적용 후 모든 잔액 \|x\| < 10^-d (d = 현지 통화 소수 자릿수. 0이면 1단위 미만) | 100% |
| 동시 편집 (GAS E2E) | 두 페이지가 서로 다른 날짜를 저장 → 서버에 두 날짜 모두 남음 | 통과 |
| 보기 전용 (GAS E2E) | 키 없는 페이지: 편집 컨트롤 0개, 서버 쓰기 호출 0건 | 통과 |
| 오프라인 (GAS E2E) | 서버 실패 → outbox 1건 → 재전송 후 중복 없이 1건 | 통과 |
| 소수 금액 | €4.50 저장·합계·정산 표시 | 통과 |
| fake 일치 | apps-script 공개 함수 = window.fake 함수 집합 | 일치 |
| 화면 | 390px 가로 스크롤 없음, 페이지 오류 0 | 통과 |

**실제 Google 환경**(Apps Script 배포, 캘린더, My Maps, Directions 대중교통)은 이 저장소에서 실행할 수 없으므로 **미확인**이다. 공개 전에 사람이 템플릿을 다시 만들고 배포해서 확인해야 한다.
