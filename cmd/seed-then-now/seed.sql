-- Idempotent Then & Now fixture data for local/dev databases.
-- These records are illustrative; replace with client-cleared content before launch.

INSERT INTO heritage_sites (
  id, name, slug, county, locality, site_type, access_mode, sensitivity, status,
  managing_authority, photography_policy, cultural_authority, consent_record,
  consent_date, site_rules, safety_notes, short_description, full_description,
  sources, fact_check, public_rights_line, latitude, longitude, geofence_radius_m,
  standing_point_note, image_key
) VALUES
(
  '10000000-0000-0000-0000-000000000001', 'Nairobi Railway Museum', 'nairobi-railway-museum',
  'Nairobi', 'Nairobi', 'historical', 'camera_video', 'standard', 'published',
  'National Museums of Kenya', 'Allowed with attribution · no tripods',
  'National Museums of Kenya', 'Community review recorded', '2026-09-12',
  'Stay behind the marked line. No climbing on locomotives. Filming for personal use is allowed.',
  'The yard is uneven. Wear closed shoes; watch for moving vehicles near the depot.',
  'The railway that built Nairobi, as it is today.',
  'The museum preserves the locomotives, carriages, engineering equipment, photographs, and artefacts of Kenya and East Africa’s railway system.',
  'NMK object labels · East African Railways archive · site visit 12 Sep 2026',
  'Signed off by the content editor · 12 Sep 2026',
  'Footage: client-supplied. Used with permission. Text: YTCI and community review.',
  -1.293613, 36.822641, 150, 'Main yard entrance, beside the information board.', 'then-now/railway.jpg'
),
(
  '10000000-0000-0000-0000-000000000002', 'Nairobi War Cemetery', 'nairobi-war-cemetery',
  'Nairobi', 'Nairobi', 'historical', 'video_only', 'sensitive', 'published',
  'Commonwealth War Graves Commission', 'Respectful photography only · no individual graves without permission',
  'CWGC · custodian guidance', 'Custodian review recorded', '2026-09-11',
  'Walk quietly. Do not film individual graves or visitors. Follow the custodian’s guidance.',
  'This is a place of remembrance. Group visits should announce themselves at the office.',
  'A place of remembrance, as it is today.',
  'The cemetery commemorates 1,952 Commonwealth service personnel who died during the Second World War, and others whose graves are unknown.',
  'CWGC cemetery record · custodian briefing · site visit 11 Sep 2026',
  'Signed off · 11 Sep 2026',
  'Footage: client-supplied. Used with permission. Photography policy agreed with CWGC.',
  -1.309866, 36.748275, 150, 'Main gate, beside the register.', 'then-now/cemetery.jpg'
),
(
  '10000000-0000-0000-0000-000000000003', 'African Heritage House', 'african-heritage-house',
  'Machakos', 'Mlolongo, Mavoko', 'cultural', 'not_available', 'standard', 'draft',
  'African Heritage House', 'Not confirmed', 'House management', '', NULL,
  'Visits by arrangement. The house is a private property and a national monument.',
  'Confirm the visitor route and photography rules before travelling.',
  'An African archive in architecture and objects.',
  'Completed in 1994, the house draws on Malian earthen structures, Moroccan mud palaces, and Swahili architecture.',
  'House notes · client description · licence not yet cleared', 'Pending rights review',
  'Rights and community consent not yet confirmed.',
  -1.400717, 36.939163, 150, 'Visitor parking, by the main house.', 'then-now/african-house.jpg'
),
(
  '10000000-0000-0000-0000-000000000004', 'Nabongo Cultural Centre', 'nabongo-cultural-centre',
  'Kakamega', 'Matungu', 'cultural', 'not_available', 'restricted', 'draft',
  'Wanga community', 'Guided visits only · sacred spaces restricted',
  'Wanga community · Nabongo cultural custodians', '', NULL,
  'Visit with a community guide. The mausoleum and sacred spaces need explicit permission.',
  'Follow the guide’s instructions. Some spaces are restricted.',
  'The Wanga story, held by the community.',
  'Also known as Eshiembekho, the centre preserves the royal heritage and indigenous knowledge of the Wanga people.',
  'Community description · client notes · consent pending', 'Pending community review',
  'Community consent not yet recorded.',
  0.391607, 34.477219, 150, 'Community reception, at the main gate.', 'then-now/nabongo.jpg'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO then_now_experiences (
  id, site_id, title, status, live_revision_number, open_revision_number
) VALUES
('20000000-0000-0000-0000-000000000001', '10000000-0000-0000-0000-000000000001', 'The railway that built Nairobi', 'published', 3, 4),
('20000000-0000-0000-0000-000000000002', '10000000-0000-0000-0000-000000000002', 'A place of remembrance', 'published', 2, 3),
('20000000-0000-0000-0000-000000000003', '10000000-0000-0000-0000-000000000003', 'An African archive', 'draft', 0, 1),
('20000000-0000-0000-0000-000000000004', '10000000-0000-0000-0000-000000000004', 'The Wanga story', 'draft', 0, 1)
ON CONFLICT (id) DO NOTHING;

INSERT INTO then_now_revisions (id, experience_id, revision_number, status, snapshot, review_notes) VALUES
(
  '30000000-0000-0000-0000-000000000001', '20000000-0000-0000-0000-000000000001', 3, 'published',
  $json${"title":"The railway that built Nairobi","promise":"See the museum yard, the locomotives, and the workshop as they are today.","story":"The Uganda Railway reached Nairobi in 1899. The museum holds the locomotives, carriages, and engineering equipment that shaped the city.","period":"1899–1971","duration":77,"contentNotes":"The video is a current-scene walk through the yard.","safetyNotes":"Stay behind the marked line.","sources":"NMK object labels · East African Railways archive","factCheck":"Signed off · 12 Sep 2026","primaryMode":"camera_video","languages":["EN","SW"],"rightsRecord":"Client footage · cleared for app and web","rightsEvidence":"Signed release · 12 Sep 2026","rightsExpiry":"2027-06-30","consentRecord":"Community review recorded · 12 Sep 2026","sensitivity":"standard","videoMaster":"then-now/railway/master.mp4","videoSize":"1.4 GB","renditions":{"1080":"Ready","720":"Ready","540":"Ready"},"poster":true,"captions":{"en":true,"sw":false},"transcript":true,"audioDescription":false,"textCards":[{"at":6,"text":"1899 — the Uganda Railway reaches Nairobi."},{"at":22,"text":"The railway helped Nairobi grow into a major urban centre."}]}$json$,
  '[]'::jsonb
),
(
  '30000000-0000-0000-0000-000000000002', '20000000-0000-0000-0000-000000000001', 4, 'draft',
  $json${"title":"The railway that built Nairobi","promise":"See the museum yard, the locomotives, and the workshop as they are today.","story":"The Uganda Railway reached Nairobi in 1899. The museum holds the locomotives, carriages, and engineering equipment that shaped the city.","period":"1899–1971","duration":77,"contentNotes":"The video is a current-scene walk through the yard.","safetyNotes":"Stay behind the marked line.","sources":"NMK object labels · East African Railways archive","factCheck":"Signed off · 12 Sep 2026","primaryMode":"camera_video","languages":["EN","SW"],"rightsRecord":"Client footage · cleared for app and web","rightsEvidence":"Signed release · 12 Sep 2026","rightsExpiry":"2027-06-30","consentRecord":"Community review recorded · 12 Sep 2026","sensitivity":"standard","videoMaster":"then-now/railway/master.mp4","videoSize":"1.4 GB","renditions":{"1080":"Ready","720":"Ready","540":"Ready"},"poster":true,"captions":{"en":true,"sw":false},"transcript":true,"audioDescription":false,"textCards":[{"at":6,"text":"1899 — the Uganda Railway reaches Nairobi."},{"at":22,"text":"The railway helped Nairobi grow into a major urban centre."},{"at":52,"text":"The museum opened in 1971 under the East African Railways and Harbours Corporation."}]}$json$,
  $json$[{"author":"Daniel M.","role":"Administrator","at":"2026-11-17T08:50:00+03:00","note":"Tighten card 02: the railway growth line repeats the promise."}]$json$
),
(
  '30000000-0000-0000-0000-000000000003', '20000000-0000-0000-0000-000000000002', 2, 'published',
  $json${"title":"A place of remembrance","promise":"A quiet, current-scene portrait of the cemetery and its custodian’s guidance.","story":"The cemetery commemorates 1,952 Commonwealth service personnel from the Second World War.","period":"1939–1945","duration":93,"contentNotes":"Sensitive site. No individual graves or visitors.","safetyNotes":"Walk quietly.","sources":"CWGC record · custodian briefing","factCheck":"Signed off · 11 Sep 2026","primaryMode":"video_only","languages":["EN","SW"],"rightsRecord":"Client footage · cleared for app and web","rightsEvidence":"Signed release · 11 Sep 2026","rightsExpiry":"2028-03-31","consentRecord":"CWGC custodian review · 11 Sep 2026","sensitivity":"sensitive","videoMaster":"then-now/cemetery/master.mp4","videoSize":"1.8 GB","renditions":{"1080":"Ready","720":"Ready","540":"Ready"},"poster":true,"captions":{"en":true,"sw":false},"transcript":true,"audioDescription":false,"textCards":[{"at":10,"text":"1,952 Commonwealth service personnel from the Second World War are commemorated here."},{"at":36,"text":"Nairobi was the headquarters of the East African Force."}]}$json$,
  '[]'::jsonb
),
(
  '30000000-0000-0000-0000-000000000004', '20000000-0000-0000-0000-000000000002', 3, 'in_review',
  $json${"title":"A place of remembrance","promise":"A quiet, current-scene portrait of the cemetery and its custodian’s guidance.","story":"The cemetery commemorates 1,952 Commonwealth service personnel from the Second World War.","period":"1939–1945","duration":93,"contentNotes":"Sensitive site. No individual graves or visitors.","safetyNotes":"Walk quietly.","sources":"CWGC record · custodian briefing","factCheck":"Signed off · 11 Sep 2026","primaryMode":"video_only","languages":["EN","SW"],"rightsRecord":"Client footage · cleared for app and web","rightsEvidence":"Signed release · 11 Sep 2026","rightsExpiry":"2028-03-31","consentRecord":"CWGC custodian review · 11 Sep 2026","sensitivity":"sensitive","videoMaster":"then-now/cemetery/master.mp4","videoSize":"1.8 GB","renditions":{"1080":"Ready","720":"Ready","540":"Ready"},"poster":true,"captions":{"en":true,"sw":false},"transcript":true,"audioDescription":false,"textCards":[{"at":10,"text":"1,952 Commonwealth service personnel from the Second World War are commemorated here."},{"at":36,"text":"Nairobi was the headquarters of the East African Force."},{"at":62,"text":"The cemetery is a place of remembrance. Filming individual graves is not permitted."}]}$json$,
  $json$[{"author":"Daniel M.","role":"Administrator","at":"2026-11-16T17:05:00+03:00","note":"Sensitive site. Please confirm the no-graves note with the custodian before approval."}]$json$
),
(
  '30000000-0000-0000-0000-000000000005', '20000000-0000-0000-0000-000000000003', 1, 'draft',
  $json${"title":"An African archive","promise":"","story":"","period":"1994","duration":102,"contentNotes":"","safetyNotes":"","sources":"House notes · client description","factCheck":"Pending rights review","primaryMode":"video_only","languages":["EN","SW"],"rightsRecord":"","rightsEvidence":"","rightsExpiry":"","consentRecord":"","sensitivity":"standard","videoMaster":"","videoSize":"","renditions":{},"poster":false,"captions":{"en":false,"sw":false},"transcript":false,"audioDescription":false,"textCards":[]}$json$,
  '[]'::jsonb
),
(
  '30000000-0000-0000-0000-000000000006', '20000000-0000-0000-0000-000000000004', 1, 'draft',
  $json${"title":"The Wanga story","promise":"","story":"","period":"Wanga Kingdom","duration":106,"contentNotes":"","safetyNotes":"","sources":"Community description · client notes","factCheck":"Pending community review","primaryMode":"video_only","languages":["EN","SW"],"rightsRecord":"","rightsEvidence":"","rightsExpiry":"","consentRecord":"","sensitivity":"restricted","videoMaster":"","videoSize":"","renditions":{},"poster":false,"captions":{"en":false,"sw":false},"transcript":false,"audioDescription":false,"textCards":[]}$json$,
  '[]'::jsonb
)
ON CONFLICT (experience_id, revision_number) DO NOTHING;

UPDATE then_now_experiences SET live_revision_number = 3, open_revision_number = 4 WHERE id = '20000000-0000-0000-0000-000000000001';
UPDATE then_now_experiences SET live_revision_number = 2, open_revision_number = 3 WHERE id = '20000000-0000-0000-0000-000000000002';

-- Media object keys for the illustrative published fixtures.
UPDATE then_now_revisions
SET snapshot = snapshot || $json${"renditionKeys":{"1080":"then-now/railway/1080.mp4","720":"then-now/railway/720.mp4","540":"then-now/railway/540.mp4"},"posterKey":"then-now/railway/poster.jpg","captionKeys":{"en":"then-now/railway/en.vtt","sw":"then-now/railway/sw.vtt"},"transcriptKey":"then-now/railway/transcript.txt","audioDescriptionKey":""}$json$
WHERE experience_id IN ('20000000-0000-0000-0000-000000000001');

UPDATE then_now_revisions
SET snapshot = snapshot || $json${"renditionKeys":{"1080":"then-now/cemetery/1080.mp4","720":"then-now/cemetery/720.mp4","540":"then-now/cemetery/540.mp4"},"posterKey":"then-now/cemetery/poster.jpg","captionKeys":{"en":"then-now/cemetery/en.vtt","sw":"then-now/cemetery/sw.vtt"},"transcriptKey":"then-now/cemetery/transcript.txt","audioDescriptionKey":""}$json$
WHERE experience_id IN ('20000000-0000-0000-0000-000000000002');

-- Illustrative audit trail for the seeded review records.
INSERT INTO then_now_audit_events (id, entity_type, entity_id, event_type, summary, created_at) VALUES
('40000000-0000-0000-0000-000000000001', 'experience', '20000000-0000-0000-0000-000000000001', 'draft', 'Draft v4 created', now() - interval '2 days'),
('40000000-0000-0000-0000-000000000002', 'experience', '20000000-0000-0000-0000-000000000001', 'save', 'Draft saved', now() - interval '1 day'),
('40000000-0000-0000-0000-000000000003', 'experience', '20000000-0000-0000-0000-000000000002', 'submit', 'Revision v3 submitted for review', now() - interval '1 day'),
('40000000-0000-0000-0000-000000000004', 'experience', '20000000-0000-0000-0000-000000000002', 'client', 'Client approval recorded', now() - interval '1 day')
ON CONFLICT (id) DO NOTHING;

UPDATE then_now_revisions
SET submitted_at = now() - interval '1 day',
    client_approval = $json${"name":"Njeri K.","role":"Client approver","at":"2026-11-16T17:02:00+03:00"}$json$
WHERE id = '30000000-0000-0000-0000-000000000004';
