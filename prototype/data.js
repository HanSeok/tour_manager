// Shared constants + SAMPLE data for the local prototype / tests (Apps Script mode loads everything from the server).
// Sample places are public landmarks with illustrative hours — not anyone's saved list.
const CATEGORIES = ['식비', '교통', '숙박', '쇼핑', '관광', '기타'];
const LAYERS = ['식당', '관광', '쇼핑', '숙소', '교통'];

const SAMPLE_CONFIG = {
  title: '샘플 여행', subtitle: '렌터카', start: '2027-04-10', end: '2027-04-14', days: 5,
  timezone: 'Asia/Tokyo', offset: '+09:00', destKeywords: ['오키나와', '나하', 'OKA'],
  localCurrency: '엔', localSymbol: '¥', homeSymbol: '₩', rate: 9.3,
  payers: ['나', '동행'], methods: ['카드', '현금'], expenseSheet: '여행_지출',
  arrivalBufferMin: 60, flightLeadMin: 90, defaultStart: '09:00', missing: [], invalid: [], hasMyMaps: true,
};

const sampleHours = (...ranges) => {
  const toMin = (s) => { const [a, b] = s.split(':').map(Number); return a * 60 + b; };
  const w = ranges.map((r) => r.split('-').map(toMin));
  return Object.fromEntries([0, 1, 2, 3, 4, 5, 6].map((d) => [d, w]));
};
const closedOn = (hours, ...days) => ({ ...hours, ...Object.fromEntries(days.map((d) => [d, []])) });
const SAMPLE_SEARCH_URL = 'https://www.google.com/maps/search/?api=1&query='; // plain string: see build.py "//" rule
const samplePlace = (id, name, layer, category, lat, lng, hours) => ({
  id, name, layer, category, lat, lng, hours, rating: null, reviews: null, price: '', memo: '',
  url: `${SAMPLE_SEARCH_URL}${lat},${lng}`,
});

const SAMPLE_PLACES = [
  samplePlace('s-airport', '나하 공항', '교통', '국제 공항', 26.2060, 127.6466, null),
  samplePlace('s-hotel', '샘플 호텔 (차탄)', '숙소', '호텔', 26.3146, 127.7568, null),
  samplePlace('s-shuri', '슈리성', '관광', '관광 명소', 26.2170, 127.7195, sampleHours('08:30-18:00')),
  samplePlace('s-kokusai', '국제거리', '쇼핑', '쇼핑 거리', 26.2150, 127.6870, sampleHours('10:00-22:00')),
  samplePlace('s-churaumi', '츄라우미 수족관', '관광', '수족관', 26.6943, 127.8779, sampleHours('08:30-18:30')),
  samplePlace('s-kouri', '코우리 대교', '관광', '관광 명소', 26.6946, 128.0228, sampleHours('00:00-24:00')),
  samplePlace('s-manzamo', '만자모', '관광', '관광 명소', 26.5050, 127.8510, sampleHours('08:00-17:30')),
  samplePlace('s-american', '아메리칸 빌리지', '쇼핑', '쇼핑몰', 26.3165, 127.7570, sampleHours('11:00-22:00')),
  samplePlace('s-steak', '스테이크집 (브레이크타임 예시)', '식당', '스테이크 전문점', 26.2145, 127.6860, sampleHours('11:00-15:00', '17:00-23:00')),
  samplePlace('s-soba', '소바집 (월 휴무 예시)', '식당', '국수 전문점', 26.6280, 127.8810, closedOn(sampleHours('10:30-16:00'), 1)),
  samplePlace('s-cafe', '해변 카페 (영업시간 미상 예시)', '식당', '카페', 26.4200, 127.7750, null),
];

// Calendar in the real Google Calendar shape (see Calendar.js toEvent_).
const MOCK_RAW_EVENTS = [
  { id: 'm1', title: '샘플항공 101 인천 → 오키나와(나하)', startDate: '2027-04-10', start: '11:25', endDate: '2027-04-10', end: '14:00', location: '' },
  { id: 'm2', title: '샘플 호텔 숙박', startDate: '2027-04-10', start: '14:00', endDate: '2027-04-14', end: '11:00',
    location: '샘플 호텔, 차탄', lat: 26.3146, lng: 127.7568 },
  { id: 'm3', title: '저녁 예약 (예시)', startDate: '2027-04-11', start: '18:00', endDate: '2027-04-11', end: '19:30', location: '', placeId: 's-steak' },
  { id: 'm4', title: '귀국편 오키나와(나하) → 인천 (예시)', startDate: '2027-04-14', start: '15:00', endDate: '2027-04-14', end: '17:30', location: '' },
];

const SEED_EXPENSES = [
  { id: '1', date: '2027-04-10', category: '교통', item: '렌터카 (예시)', amount: 28000, payer: '나', method: '카드' },
  { id: '2', date: '2027-04-10', category: '식비', item: '공항 편의점 (예시)', amount: 1240, payer: '동행', method: '현금' },
];
