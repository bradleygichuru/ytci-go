-- Idempotent Then & Now fixture data for local/dev databases.
-- Fixed IDs identify the four seed rows; each INSERT upserts only those rows.
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
  'The museum preserves the locomotives, carriages, engineering equipment, photographs, and artefacts of Kenya and East Africa’s railway system. The Uganda Railway reached Nairobi in 1899 and helped shape the city’s growth.',
  'NMK object labels · East African Railways archive · site visit 12 Sep 2026',
  'Signed off by the content editor · 12 Sep 2026',
  'Footage: client-supplied. Used with permission. Text: YTCI and community review.',
  -1.2936132, 36.8226414, 150, 'Main yard entrance, beside the information board.', 'then-now/railway.jpg'
),
(
  '10000000-0000-0000-0000-000000000002', 'Nairobi War Cemetery', 'nairobi-war-cemetery',
  'Nairobi', 'Nairobi', 'historical', 'video_only', 'sensitive', 'published',
  'Commonwealth War Graves Commission', 'Respectful photography only · no individual graves without permission',
  'CWGC · custodian guidance', 'Custodian review recorded', '2026-09-11',
  'Walk quietly. Do not film individual graves or visitors. Follow the custodian’s guidance.',
  'This is a place of remembrance. Group visits should announce themselves at the office.',
  'A place of remembrance, as it is today.',
  'The cemetery commemorates 1,952 Commonwealth service personnel who died during the Second World War, and others whose graves are unknown. Nairobi was the headquarters of the East African Force and an important military medical centre.',
  'CWGC cemetery record · custodian briefing · site visit 11 Sep 2026',
  'Signed off · 11 Sep 2026',
  'Footage: client-supplied. Used with permission. Photography policy agreed with CWGC.',
  -1.3098655, 36.7482745, 150, 'Main gate, beside the register.', 'then-now/cemetery.jpg'
),
(
  '10000000-0000-0000-0000-000000000003', 'African Heritage House', 'african-heritage-house',
  'Machakos', 'Mlolongo, Mavoko', 'cultural', 'not_available', 'standard', 'published',
  'African Heritage House', 'Not confirmed', 'House management',
  'Permission pending · house management review not yet recorded', NULL,
  'Visits by arrangement. The house is a private property and a national monument.',
  'Confirm the visitor route and photography rules before travelling.',
  'An African archive in architecture and objects.',
  'Completed in 1994, the house draws on Malian earthen structures, Moroccan mud palaces, and Swahili architecture. Its collection includes textiles, jewellery, pottery, ceremonial clothing, carved objects, and furniture.',
  'House notes · client description · licence not yet cleared', 'Pending rights review',
  'Permission pending. Footage and text are illustrative pending rights and house-management consent.',
  -1.4007172, 36.9391632, 150, 'Visitor parking, by the main house.', 'then-now/african-house.jpg'
),
(
  '10000000-0000-0000-0000-000000000004', 'Nabongo Cultural Centre', 'nabongo-cultural-centre',
  'Kakamega', 'Matungu', 'cultural', 'not_available', 'standard', 'published',
  'Wanga community', 'Guided visits only · sacred spaces restricted',
  'Wanga community · Nabongo cultural custodians',
  'Permission pending · community consent not yet recorded', NULL,
  'Visit with a community guide. The mausoleum and sacred spaces need explicit permission.',
  'Follow the guide’s instructions. Some spaces are restricted.',
  'The Wanga story, held by the community.',
  'Also known as Eshiembekho, the centre preserves the royal heritage and indigenous knowledge of the Wanga people. It holds royal regalia, tools, weapons, and artefacts, and includes a mausoleum and sacred spaces.',
  'Community description · client notes · consent pending', 'Pending community review',
  'Permission pending. Footage and text are illustrative pending community consent.',
  0.391607, 34.4772192, 150, 'Community reception, at the main gate.', 'then-now/nabongo.jpg'
)
ON CONFLICT (id) DO UPDATE SET
  name = EXCLUDED.name,
  slug = EXCLUDED.slug,
  county = EXCLUDED.county,
  locality = EXCLUDED.locality,
  site_type = EXCLUDED.site_type,
  access_mode = EXCLUDED.access_mode,
  sensitivity = EXCLUDED.sensitivity,
  status = EXCLUDED.status,
  managing_authority = EXCLUDED.managing_authority,
  photography_policy = EXCLUDED.photography_policy,
  cultural_authority = EXCLUDED.cultural_authority,
  consent_record = EXCLUDED.consent_record,
  consent_date = EXCLUDED.consent_date,
  site_rules = EXCLUDED.site_rules,
  safety_notes = EXCLUDED.safety_notes,
  short_description = EXCLUDED.short_description,
  full_description = EXCLUDED.full_description,
  sources = EXCLUDED.sources,
  fact_check = EXCLUDED.fact_check,
  public_rights_line = EXCLUDED.public_rights_line,
  latitude = EXCLUDED.latitude,
  longitude = EXCLUDED.longitude,
  geofence_radius_m = EXCLUDED.geofence_radius_m,
  standing_point_note = EXCLUDED.standing_point_note,
  image_key = EXCLUDED.image_key,
  updated_at = now();

INSERT INTO then_now_experiences (
  id, site_id, title, status, live_revision_number, open_revision_number
) VALUES
('20000000-0000-0000-0000-000000000001', '10000000-0000-0000-0000-000000000001', 'The railway that built Nairobi', 'published', 3, 4),
('20000000-0000-0000-0000-000000000002', '10000000-0000-0000-0000-000000000002', 'A place of remembrance', 'published', 2, 3),
('20000000-0000-0000-0000-000000000003', '10000000-0000-0000-0000-000000000003', 'An African archive', 'published', 1, 1),
('20000000-0000-0000-0000-000000000004', '10000000-0000-0000-0000-000000000004', 'The Wanga story', 'published', 1, 1)
ON CONFLICT (id) DO UPDATE SET
  site_id = EXCLUDED.site_id,
  title = EXCLUDED.title,
  status = EXCLUDED.status,
  live_revision_number = EXCLUDED.live_revision_number,
  open_revision_number = EXCLUDED.open_revision_number,
  updated_at = now();

INSERT INTO then_now_revisions (id, experience_id, revision_number, status, snapshot, review_notes) VALUES
(
  '30000000-0000-0000-0000-000000000001', '20000000-0000-0000-0000-000000000001', 3, 'published',
  $json${"title":"The railway that built Nairobi","promise":"See the museum yard, the locomotives, and the workshop as they are today.","story":"The Uganda Railway reached Nairobi in 1899. The museum holds the locomotives, carriages, and engineering equipment that shaped the city.","period":"1899–1971","duration":77,"contentNotes":"The video is a current-scene walk through the yard.","safetyNotes":"Stay behind the marked line.","sources":"NMK object labels · East African Railways archive","factCheck":"Signed off · 12 Sep 2026","primaryMode":"camera_video","languages":["EN","SW"],"rightsRecord":"Client footage · cleared for app and web","rightsEvidence":"Signed release · 12 Sep 2026","rightsExpiry":"2027-06-30","consentRecord":"Community review recorded · 12 Sep 2026","sensitivity":"standard","videoMaster":"then-now/nairobi-railway-museum/master.mp4","videoSize":"1.4 GB","renditions":{"1080":"Ready","720":"Ready","540":"Ready"},"renditionKeys":{"1080":"then-now/nairobi-railway-museum/site-1080.mp4","720":"then-now/nairobi-railway-museum/site-720.mp4","540":"then-now/nairobi-railway-museum/site-540.mp4"},"poster":true,"posterKey":"then-now/nairobi-railway-museum/poster.jpg","portalKey":"then-now/nairobi-railway-museum/plate-720.mp4","portalDuration":15,"captions":{"en":true,"sw":false},"captionKeys":{"en":"then-now/nairobi-railway-museum/en.vtt","sw":"then-now/nairobi-railway-museum/sw.vtt"},"transcript":true,"transcriptKey":"then-now/nairobi-railway-museum/transcript.txt","audioDescription":false,"audioDescriptionKey":"","textCards":[{"at":6,"text":"1899 — the Uganda Railway reaches Nairobi."},{"at":22,"text":"The railway helped Nairobi grow into a major urban centre."},{"at":52,"text":"The museum opened in 1971 under the East African Railways and Harbours Corporation."}],"shortDescription":"The railway that built Nairobi, as it is today.","fullDescription":"The museum preserves the locomotives, carriages, engineering equipment, photographs, and artefacts of Kenya and East Africa’s railway system. The Uganda Railway reached Nairobi in 1899 and helped shape the city’s growth.","latitude":-1.2936132,"longitude":36.8226414,"geofenceRadiusM":150,"publicRightsLine":"Footage: client-supplied. Used with permission. Text: YTCI and community review."}$json$,
  '[]'::jsonb
),
(
  '30000000-0000-0000-0000-000000000002', '20000000-0000-0000-0000-000000000001', 4, 'draft',
  $json${"title":"The railway that built Nairobi","promise":"See the museum yard, the locomotives, and the workshop as they are today.","story":"The Uganda Railway reached Nairobi in 1899. The museum holds the locomotives, carriages, and engineering equipment that shaped the city.","period":"1899–1971","duration":77,"contentNotes":"The video is a current-scene walk through the yard.","safetyNotes":"Stay behind the marked line.","sources":"NMK object labels · East African Railways archive","factCheck":"Signed off · 12 Sep 2026","primaryMode":"camera_video","languages":["EN","SW"],"rightsRecord":"Client footage · cleared for app and web","rightsEvidence":"Signed release · 12 Sep 2026","rightsExpiry":"2027-06-30","consentRecord":"Community review recorded · 12 Sep 2026","sensitivity":"standard","videoMaster":"then-now/nairobi-railway-museum/master.mp4","videoSize":"1.4 GB","renditions":{"1080":"Ready","720":"Ready","540":"Ready"},"renditionKeys":{"1080":"then-now/nairobi-railway-museum/site-1080.mp4","720":"then-now/nairobi-railway-museum/site-720.mp4","540":"then-now/nairobi-railway-museum/site-540.mp4"},"poster":true,"posterKey":"then-now/nairobi-railway-museum/poster.jpg","portalKey":"then-now/nairobi-railway-museum/plate-720.mp4","portalDuration":15,"captions":{"en":true,"sw":false},"captionKeys":{"en":"then-now/nairobi-railway-museum/en.vtt","sw":"then-now/nairobi-railway-museum/sw.vtt"},"transcript":true,"transcriptKey":"then-now/nairobi-railway-museum/transcript.txt","audioDescription":false,"audioDescriptionKey":"","textCards":[{"at":6,"text":"1899 — the Uganda Railway reaches Nairobi."},{"at":22,"text":"The railway helped Nairobi grow into a major urban centre."},{"at":52,"text":"The museum opened in 1971 under the East African Railways and Harbours Corporation."}],"shortDescription":"The railway that built Nairobi, as it is today.","fullDescription":"The museum preserves the locomotives, carriages, engineering equipment, photographs, and artefacts of Kenya and East Africa’s railway system. The Uganda Railway reached Nairobi in 1899 and helped shape the city’s growth.","latitude":-1.2936132,"longitude":36.8226414,"geofenceRadiusM":150,"publicRightsLine":"Footage: client-supplied. Used with permission. Text: YTCI and community review."}$json$,
  $json$[{"author":"Daniel M.","role":"Administrator","at":"2026-11-17T08:50:00+03:00","note":"Tighten card 02: the railway growth line repeats the promise."}]$json$
),
(
  '30000000-0000-0000-0000-000000000003', '20000000-0000-0000-0000-000000000002', 2, 'published',
  $json${"title":"A place of remembrance","promise":"A quiet, current-scene portrait of the cemetery and its custodian’s guidance.","story":"The cemetery commemorates 1,952 Commonwealth service personnel from the Second World War.","period":"1939–1945","duration":93,"contentNotes":"Sensitive site. No individual graves or visitors.","safetyNotes":"Walk quietly.","sources":"CWGC record · custodian briefing","factCheck":"Signed off · 11 Sep 2026","primaryMode":"video_only","languages":["EN","SW"],"rightsRecord":"Client footage · cleared for app and web","rightsEvidence":"Signed release · 11 Sep 2026","rightsExpiry":"2028-03-31","consentRecord":"CWGC custodian review · 11 Sep 2026","sensitivity":"sensitive","videoMaster":"then-now/nairobi-war-cemetery/master.mp4","videoSize":"1.8 GB","renditions":{"1080":"Ready","720":"Ready","540":"Ready"},"renditionKeys":{"1080":"then-now/nairobi-war-cemetery/site-1080.mp4","720":"then-now/nairobi-war-cemetery/site-720.mp4","540":"then-now/nairobi-war-cemetery/site-540.mp4"},"poster":true,"posterKey":"then-now/nairobi-war-cemetery/poster.jpg","captions":{"en":true,"sw":false},"captionKeys":{"en":"then-now/nairobi-war-cemetery/en.vtt","sw":"then-now/nairobi-war-cemetery/sw.vtt"},"transcript":true,"transcriptKey":"then-now/nairobi-war-cemetery/transcript.txt","audioDescription":false,"audioDescriptionKey":"","textCards":[{"at":10,"text":"1,952 Commonwealth service personnel from the Second World War are commemorated here."},{"at":36,"text":"Nairobi was the headquarters of the East African Force and an important military medical centre."},{"at":62,"text":"The cemetery is a place of remembrance. Filming individual graves is not permitted."}],"shortDescription":"A place of remembrance, as it is today.","fullDescription":"The cemetery commemorates 1,952 Commonwealth service personnel who died during the Second World War, and others whose graves are unknown. Nairobi was the headquarters of the East African Force and an important military medical centre.","latitude":-1.3098655,"longitude":36.7482745,"geofenceRadiusM":150,"publicRightsLine":"Footage: client-supplied. Used with permission. Photography policy agreed with CWGC."}$json$,
  '[]'::jsonb
),
(
  '30000000-0000-0000-0000-000000000004', '20000000-0000-0000-0000-000000000002', 3, 'in_review',
  $json${"title":"A place of remembrance","promise":"A quiet, current-scene portrait of the cemetery and its custodian’s guidance.","story":"The cemetery commemorates 1,952 Commonwealth service personnel from the Second World War.","period":"1939–1945","duration":93,"contentNotes":"Sensitive site. No individual graves or visitors.","safetyNotes":"Walk quietly.","sources":"CWGC record · custodian briefing","factCheck":"Signed off · 11 Sep 2026","primaryMode":"video_only","languages":["EN","SW"],"rightsRecord":"Client footage · cleared for app and web","rightsEvidence":"Signed release · 11 Sep 2026","rightsExpiry":"2028-03-31","consentRecord":"CWGC custodian review · 11 Sep 2026","sensitivity":"sensitive","videoMaster":"then-now/nairobi-war-cemetery/master.mp4","videoSize":"1.8 GB","renditions":{"1080":"Ready","720":"Ready","540":"Ready"},"renditionKeys":{"1080":"then-now/nairobi-war-cemetery/site-1080.mp4","720":"then-now/nairobi-war-cemetery/site-720.mp4","540":"then-now/nairobi-war-cemetery/site-540.mp4"},"poster":true,"posterKey":"then-now/nairobi-war-cemetery/poster.jpg","captions":{"en":true,"sw":false},"captionKeys":{"en":"then-now/nairobi-war-cemetery/en.vtt","sw":"then-now/nairobi-war-cemetery/sw.vtt"},"transcript":true,"transcriptKey":"then-now/nairobi-war-cemetery/transcript.txt","audioDescription":false,"audioDescriptionKey":"","textCards":[{"at":10,"text":"1,952 Commonwealth service personnel from the Second World War are commemorated here."},{"at":36,"text":"Nairobi was the headquarters of the East African Force and an important military medical centre."},{"at":62,"text":"The cemetery is a place of remembrance. Filming individual graves is not permitted."}],"shortDescription":"A place of remembrance, as it is today.","fullDescription":"The cemetery commemorates 1,952 Commonwealth service personnel who died during the Second World War, and others whose graves are unknown. Nairobi was the headquarters of the East African Force and an important military medical centre.","latitude":-1.3098655,"longitude":36.7482745,"geofenceRadiusM":150,"publicRightsLine":"Footage: client-supplied. Used with permission. Photography policy agreed with CWGC."}$json$,
  $json$[{"author":"Daniel M.","role":"Administrator","at":"2026-11-16T17:05:00+03:00","note":"Sensitive site. Please confirm the no-graves note with the custodian before approval."}]$json$
),
(
  '30000000-0000-0000-0000-000000000005', '20000000-0000-0000-0000-000000000003', 1, 'published',
  $json${"title":"An African archive","promise":"See the house and its collection as they are today.","story":"Completed in 1994, the house draws on Malian earthen structures, Moroccan mud palaces, and Swahili architecture. Its collection includes textiles, jewellery, pottery, ceremonial clothing, carved objects, and furniture.","period":"1994","duration":102,"contentNotes":"Illustrative current-scene edit. Site permission and photography clearance are pending.","safetyNotes":"Confirm the visitor route and photography rules before travelling.","sources":"House notes · client description","factCheck":"Pending rights review","primaryMode":"video_only","languages":["EN","SW"],"rightsRecord":"Permission pending · rights review not complete","rightsEvidence":"No release recorded","rightsExpiry":"","consentRecord":"Permission pending · house management review not yet recorded","sensitivity":"standard","videoMaster":"then-now/african-heritage-house/master.mp4","videoSize":"1.1 GB","renditions":{"1080":"Ready","720":"Ready","540":"Ready"},"renditionKeys":{"1080":"then-now/african-heritage-house/site-1080.mp4","720":"then-now/african-heritage-house/site-720.mp4","540":"then-now/african-heritage-house/site-540.mp4"},"poster":true,"posterKey":"then-now/african-heritage-house/poster.jpg","captions":{"en":true,"sw":false},"captionKeys":{"en":"then-now/african-heritage-house/en.vtt","sw":"then-now/african-heritage-house/sw.vtt"},"transcript":true,"transcriptKey":"then-now/african-heritage-house/transcript.txt","audioDescription":false,"audioDescriptionKey":"","textCards":[{"at":8,"text":"Completed in 1994, the house draws on Malian, Moroccan, and Swahili architecture."},{"at":34,"text":"The collection includes textiles, jewellery, pottery, ceremonial clothing, and carved objects."},{"at":60,"text":"The house is a national monument overlooking Nairobi National Park."}],"shortDescription":"An African archive in architecture and objects.","fullDescription":"Completed in 1994, the house draws on Malian earthen structures, Moroccan mud palaces, and Swahili architecture. Its collection includes textiles, jewellery, pottery, ceremonial clothing, carved objects, and furniture.","latitude":-1.4007172,"longitude":36.9391632,"geofenceRadiusM":150,"publicRightsLine":"Permission pending. Footage and text are illustrative pending rights and house-management consent."}$json$,
  '[]'::jsonb
),
(
  '30000000-0000-0000-0000-000000000006', '20000000-0000-0000-0000-000000000004', 1, 'published',
  $json${"title":"The Wanga story","promise":"See the Wanga story as it is held by the community today.","story":"Also known as Eshiembekho, the centre preserves the royal heritage and indigenous knowledge of the Wanga people. It holds royal regalia, tools, weapons, and artefacts, and includes a mausoleum and sacred spaces.","period":"Wanga Kingdom","duration":106,"contentNotes":"Illustrative current-scene edit. Community consent for the mausoleum and sacred spaces is pending.","safetyNotes":"Follow the guide’s instructions. Some spaces are restricted.","sources":"Community description · client notes","factCheck":"Pending community review","primaryMode":"video_only","languages":["EN","SW"],"rightsRecord":"Permission pending · community consent not complete","rightsEvidence":"No consent record","rightsExpiry":"","consentRecord":"Permission pending · community consent not yet recorded","sensitivity":"standard","videoMaster":"then-now/nabongo-cultural-centre/master.mp4","videoSize":"1.2 GB","renditions":{"1080":"Ready","720":"Ready","540":"Ready"},"renditionKeys":{"1080":"then-now/nabongo-cultural-centre/site-1080.mp4","720":"then-now/nabongo-cultural-centre/site-720.mp4","540":"then-now/nabongo-cultural-centre/site-540.mp4"},"poster":true,"posterKey":"then-now/nabongo-cultural-centre/poster.jpg","captions":{"en":true,"sw":false},"captionKeys":{"en":"then-now/nabongo-cultural-centre/en.vtt","sw":"then-now/nabongo-cultural-centre/sw.vtt"},"transcript":true,"transcriptKey":"then-now/nabongo-cultural-centre/transcript.txt","audioDescription":false,"audioDescriptionKey":"","textCards":[{"at":8,"text":"Also known as Eshiembekho, the centre preserves the heritage of the Wanga Kingdom."},{"at":34,"text":"The museum holds royal regalia, tools, weapons, and artefacts."},{"at":62,"text":"The mausoleum and sacred spaces are visited with a guide and the community’s permission."}],"shortDescription":"The Wanga story, held by the community.","fullDescription":"Also known as Eshiembekho, the centre preserves the royal heritage and indigenous knowledge of the Wanga people. It holds royal regalia, tools, weapons, and artefacts, and includes a mausoleum and sacred spaces.","latitude":0.391607,"longitude":34.4772192,"geofenceRadiusM":150,"publicRightsLine":"Permission pending. Footage and text are illustrative pending community consent."}$json$,
  '[]'::jsonb
)
ON CONFLICT (experience_id, revision_number) DO UPDATE SET
  status = EXCLUDED.status,
  snapshot = EXCLUDED.snapshot,
  review_notes = EXCLUDED.review_notes,
  updated_at = now();

-- Illustrative audit trail for the seeded review records.
INSERT INTO then_now_audit_events (id, entity_type, entity_id, event_type, summary, created_at) VALUES
('40000000-0000-0000-0000-000000000001', 'experience', '20000000-0000-0000-0000-000000000001', 'draft', 'Draft v4 created', now() - interval '2 days'),
('40000000-0000-0000-0000-000000000002', 'experience', '20000000-0000-0000-0000-000000000001', 'save', 'Draft saved', now() - interval '1 day'),
('40000000-0000-0000-0000-000000000003', 'experience', '20000000-0000-0000-0000-000000000002', 'submit', 'Revision v3 submitted for review', now() - interval '1 day'),
('40000000-0000-0000-0000-000000000004', 'experience', '20000000-0000-0000-0000-000000000002', 'client', 'Client approval recorded', now() - interval '1 day'),
('40000000-0000-0000-0000-000000000005', 'experience', '20000000-0000-0000-0000-000000000003', 'publish', 'Published live revision v1', now() - interval '2 days'),
('40000000-0000-0000-0000-000000000006', 'experience', '20000000-0000-0000-0000-000000000004', 'publish', 'Published live revision v1', now() - interval '2 days')
ON CONFLICT (id) DO NOTHING;

UPDATE then_now_revisions
SET submitted_at = now() - interval '1 day',
    client_approval = $json${"name":"Njeri K.","role":"Client approver","at":"2026-11-16T17:02:00+03:00"}$json$
WHERE id = '30000000-0000-0000-0000-000000000004';
