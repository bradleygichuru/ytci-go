-- Golden destination fixtures: the 10 Home adventure carousel destinations.
--
-- Run after 0017_golden_destination_fields.sql. Idempotent: upserts by slug,
-- archives uncurated aliases/duplicates, and sets is_curated=true.
-- Unknown facts are recorded as not_verified/unverified, never guessed.

BEGIN;

CREATE TEMP TABLE golden_destinations (
    slug TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    county TEXT NOT NULL,
    county_code TEXT NOT NULL,
    locality TEXT,
    category TEXT NOT NULL,
    interest_tags TEXT[] NOT NULL,
    latitude DOUBLE PRECISION NOT NULL,
    longitude DOUBLE PRECISION NOT NULL,
    short_description TEXT NOT NULL,
    full_description TEXT,
    significance TEXT,
    history TEXT,
    things_to_do TEXT,
    suitable_audiences TEXT,
    duration TEXT,
    difficulty TEXT,
    seasonality TEXT,
    indicative_fees TEXT,
    opening_info TEXT,
    transport_notes TEXT,
    accessibility TEXT,
    facilities TEXT,
    safety_notes TEXT,
    source TEXT NOT NULL,
    content_owner TEXT NOT NULL,
    review_date TIMESTAMP NOT NULL,
    visit_duration_min_minutes INTEGER NOT NULL,
    visit_duration_max_minutes INTEGER NOT NULL,
    fee_status TEXT NOT NULL,
    fee_currency TEXT,
    fee_amount INTEGER,
    fee_basis TEXT NOT NULL,
    opening_hours_status TEXT NOT NULL,
    opening_hours JSONB,
    hero_image_url TEXT,
    hero_image_attribution TEXT NOT NULL,
    hero_image_license TEXT,
    hero_image_source TEXT,
    hero_image_alt TEXT NOT NULL
);

INSERT INTO golden_destinations (slug, name, county, county_code, locality, category, interest_tags, latitude, longitude, short_description, full_description, significance, history, things_to_do, suitable_audiences, duration, difficulty, seasonality, indicative_fees, opening_info, transport_notes, accessibility, facilities, safety_notes, source, content_owner, review_date, visit_duration_min_minutes, visit_duration_max_minutes, fee_status, fee_currency, fee_amount, fee_basis, opening_hours_status, opening_hours, hero_image_url, hero_image_attribution, hero_image_license, hero_image_source, hero_image_alt) VALUES
    ('hells-kitchen', 'Hell''s Kitchen', 'Kilifi', '003', 'Marafa', 'Sightseeing', ARRAY['Sightseeing', 'Hiking', 'Photography', 'Culture'], -3.0167, 39.8333, 'A red sandstone gorge and badlands landscape near Marafa, shaped by erosion and woven into Giriama stories.', 'Hell''s Kitchen, also known as Marafa Depression, is a network of red sandstone gorges, ridges, and natural pillars in Kilifi County. Erosion has carved the soft beds into narrow valleys and viewpoints. Local Giriama guides share the site''s origin stories and the community''s connection to the land. Visits are usually on foot along the rim and into the gorges.', 'A distinctive red-sandstone badlands landscape and a community-guided cultural site in Magarini, Kilifi County.', 'Local Giriama tradition gives the depression its name and origin story; the landform itself is the result of long-term erosion.', 'Walk the rim viewpoints; descend into the gorges with a community guide; photograph the red cliffs in late afternoon light; learn the site''s Giriama stories.', 'Photographers, hikers, cultural travellers', '2–3 hours', 'moderate', 'Best in the dry season; late afternoon light is strongest. Confirm local conditions before travelling.', 'Community entrance reported at KES 200 per person plus a guide fee (reported KES 500). No official published tariff verified.', 'No official published hours verified. Visits are typically daytime; sunset is the popular time.', 'About 30–45 minutes from Malindi by road, then a dirt track to the site.', 'Not verified. Open, uneven gorge terrain; no formal accessible route confirmed.', 'Community guide station; basic facilities only. Confirm locally.', 'Not verified. Follow the community guide and stay on established paths.', 'YTCI field research; Green Heart Kilifi; community-reported fees (2026).', 'YTCI Content Team', now() + interval '6 months', 120, 180, 'reported', 'KES', 200, 'Community entrance reported at KES 200 per person plus a guide fee (reported KES 500); not an official published tariff. Confirm at the gate.', 'unverified', NULL, NULL, 'YTCI', 'YTCI', 'YTCI bundled asset', 'Red sandstone gorges at Hell''s Kitchen, Marafa.'),
    ('chalbi-desert', 'Chalbi Desert', 'Marsabit', '010', 'Chalbi', 'Activity', ARRAY['Activity', 'Photography', 'Culture', 'Sightseeing'], 2.75, 37.5, 'A vast salt pan and desert basin east of Lake Turkana, with cracked mud flats, low dunes, and heat shimmer.', 'The Chalbi Desert is a saline desert basin in Marsabit County, east of Lake Turkana and west of the Marsabit highlands. Its flat salt pans, seasonal shallow lakes, and scattered volcanic hills create a stark landscape. The area is traversed by remote 4x4 routes linking Marsabit, North Horr, and the Turkana side. Pastoralist communities use the surrounding rangelands.', 'One of Kenya''s largest desert basins and a dramatic overland route in northern Kenya.', 'The basin is the bed of a former shallow lake; seasonal rains can briefly flood parts of the pan.', '4x4 crossings; landscape photography; stargazing; cultural stops with local guides; birdlife after rains.', 'Overland travellers, photographers, adventure travellers', 'Half-day to full-day crossing', 'hard', 'Access depends on recent rain; confirm road conditions locally.', 'No gated entrance fee. Transport, guide, and community fees vary.', 'Open landscape; no gate. Daylight travel recommended.', 'Reached by 4x4 from Marsabit or North Horr; no scheduled public transport on the pans.', 'Not verified. Remote desert terrain with no formal accessible facilities.', 'None on the pan; supplies in Marsabit or North Horr.', 'Not verified. Travel with a local guide and a well-prepared 4x4; carry water and communications.', 'YTCI field research; Heartland Safaris; Samburu Reserve guide; northern Kenya travel guides (2026).', 'YTCI Content Team', now() + interval '6 months', 240, 480, 'free', NULL, 0, 'No official gated entrance fee; the desert is open land. Guide, transport, and community fees are separate.', 'verified', '{"always_open": true, "note": "Open desert; no gate. Daylight visits recommended."}'::jsonb, NULL, 'YTCI', 'YTCI', 'YTCI bundled asset', 'Salt pan and dunes in the Chalbi Desert, Marsabit.'),
    ('lake-paradise', 'Lake Paradise', 'Marsabit', '010', 'Marsabit National Park', 'Wildlife', ARRAY['Wildlife', 'Conservation', 'Photography', 'Hiking'], 2.3167, 37.9833, 'A crater lake inside Marsabit National Park, ringed by montane forest and known for birdlife and the park''s elephant history.', 'Lake Paradise is one of the crater lakes in Marsabit National Park, set in forested highlands above the surrounding desert. The lake and its surrounding forest support birdlife and wildlife, and the park is known for its elephant population and the legacy of large-tusked bulls. Access is through the national park.', 'A key crater-lake and forest habitat within Marsabit National Park, a KWS special-interest park.', 'Marsabit National Park was established in 1949; Lake Paradise is one of its three crater lakes.', 'Birdwatching; forest walks; game viewing; photography; visiting the crater lakes.', 'Wildlife watchers, birders, photographers, hikers', '2–4 hours', 'moderate', 'Access and visibility vary with rain; confirm park conditions locally.', 'KWS Marsabit National Park conservation fee: non-resident adult USD 40/day, child/student USD 20; East African citizen adult KES 500, child 250; Kenya resident adult KES 675, child 350.', 'Park gate hours not independently verified; confirm with KWS Marsabit.', 'Inside Marsabit National Park, reached by road from Marsabit town; a park vehicle and guide are recommended.', 'Not verified. Forest and park terrain; no formal accessible route confirmed.', 'Park gates, ranger posts, and viewpoints; confirm current facilities with KWS.', 'Not verified. Follow KWS park rules and ranger guidance.', 'KWS Conservation Fee (Oct 2025); KWS Marsabit National Park; YTCI field research.', 'YTCI Content Team', now() + interval '6 months', 120, 240, 'verified', 'USD', 40, 'KWS Marsabit National Park (special interest) conservation fee: non-resident adult USD 40/day; child/student USD 20. East African citizen adult KES 500; Kenya resident adult KES 675. Per person per day.', 'unverified', NULL, NULL, 'YTCI', 'YTCI', 'YTCI bundled asset', 'Lake Paradise crater lake and forest in Marsabit National Park.'),
    ('lake-turkana', 'Lake Turkana', 'Turkana', '023', 'Lake Turkana', 'Sightseeing', ARRAY['Sightseeing', 'Culture', 'Photography', 'Wildlife', 'Conservation'], 3.5, 36.0, 'The world''s largest desert lake, stretching along Kenya''s northern Rift Valley with volcanic shores, islands, and Turkana communities.', 'Lake Turkana is a vast alkaline lake in the northern Rift Valley, shared between Turkana and Marsabit counties. Its jade-green water, volcanic islands, and arid shores form part of a UNESCO World Heritage site. The lake supports fisheries and the livelihoods of Turkana communities, and its islands are important breeding sites for Nile crocodiles and birds.', 'Lake Turkana National Parks are a UNESCO World Heritage Site; the lake is the world''s largest desert lake.', 'The lake has been a focus of palaeoanthropological research, including sites around Koobi Fora on the eastern shore.', 'View the lake at Eliye Springs or Loiyangalani; boat trips; fishing villages; birdlife; cultural visits; photography.', 'Adventure travellers, photographers, cultural travellers, birders', 'Half-day to full-day; multi-day for the full lake', 'moderate', 'Very hot; plan around early morning and late afternoon.', 'No single lake-wide entrance fee. Access, boat, guide, and community fees vary by route and site.', 'Open landscape and shoreline; no single gate. Some sites and conservancies have their own hours.', 'Usually reached by road from Lodwar or Marsabit; charter flights serve Loiyangalani and Lodwar.', 'Not verified. Remote shoreline and rough roads; no formal accessible facilities confirmed.', 'Lodges and camps at Loiyangalani and Eliye Springs; supplies in Lodwar.', 'Not verified. Travel with local guidance and confirm lake and road conditions.', 'UNESCO Lake Turkana National Parks; YTCI field research; northern Kenya travel guides.', 'YTCI Content Team', now() + interval '6 months', 240, 480, 'not_verified', NULL, NULL, 'No single lake-wide entrance fee verified; access, boat, guide, and community fees vary by site.', 'unverified', NULL, NULL, 'YTCI', 'YTCI', 'YTCI bundled asset', 'Shoreline and volcanic hills at Lake Turkana.'),
    ('fort-jesus', 'Fort Jesus', 'Mombasa', '001', 'Mombasa Island', 'Culture', ARRAY['Culture', 'Sightseeing', 'Photography'], -4.0625, 39.6794, 'A 16th-century Portuguese fort on Mombasa Island and a UNESCO World Heritage Site, now a museum of Swahili coast history.', 'Fort Jesus was built by the Portuguese between 1593 and 1596 to protect the port of Mombasa. Its Renaissance military design, massive walls, and five bastions have survived centuries of contestation between Portuguese, Arab, Swahili, and British powers. It is a UNESCO World Heritage Site and today houses a museum managed by the National Museums of Kenya.', 'UNESCO World Heritage Site; one of the most outstanding preserved examples of 16th-century Portuguese military fortification.', 'Built 1593–1596; changed hands many times among Portuguese, Arab, Swahili, and British control; declared a UNESCO World Heritage Site in 2011.', 'Explore the bastions and ramparts; visit the museum exhibits; join a guided tour; photograph the architecture and sea views.', 'History lovers, culture travellers, photographers, families', '1–2 hours', 'easy', 'Year-round; the coast is hot and humid.', 'NMK admission: adult KES 550 (Kenya/EAC), USD 9 (residents/rest of Africa), USD 17 (foreigners outside Africa); child KES 300 / USD 6 / USD 8. Combined Fort Jesus + Butterfly House: adult KES 650 / USD 12 / USD 19.', 'Daily 8:30 AM–5:30 PM, including public holidays.', 'On Mombasa Island, accessible by road from the city centre and Likoni Ferry.', 'Not verified. Historic fort with stairs, uneven surfaces, and narrow passages; confirm accessible routes with NMK.', 'Museum galleries, ticket office, guides, and toilets; confirm current facilities with NMK.', 'Not verified. Follow museum staff guidance.', 'National Museums of Kenya 2026 admission rates; UNESCO World Heritage Centre.', 'YTCI Content Team', now() + interval '6 months', 60, 120, 'verified', 'KES', 550, 'NMK adult admission: KES 550 (Kenya/EAC), USD 9 (residents/rest of Africa), USD 17 (foreigners outside Africa); child rates lower. Combined Fort Jesus + Butterfly House adult KES 650 / USD 12 / USD 19. Per person.', 'verified', '{"monday_sunday": "08:30-17:30", "public_holidays": true}'::jsonb, 'https://upload.wikimedia.org/wikipedia/commons/3/3a/Fort_Jesus%2C_Window_To_The_Sea.jpg', 'The Wandering Angel', 'CC BY 2.0', 'Wikimedia Commons', 'Fort Jesus walls and sea window, Mombasa.'),
    ('diani-beach', 'Diani Beach', 'Kwale', '002', 'Diani', 'Beach', ARRAY['Beach', 'Rest', 'Wildlife', 'Conservation'], -4.3222, 39.575, 'A long white-sand beach on Kenya''s south coast, backed by coral cliffs and coastal forest, with a reef lagoon.', 'Diani Beach is a 17-kilometre stretch of white coral-derived sand on Kenya''s south coast. A fringing reef creates a shallow lagoon, and the beach is backed by coastal forest and casuarina woodland. It is the gateway to the Diani-Chale Marine National Reserve and Kisite-Mpunguti Marine National Park.', 'Gateway to Diani-Chale Marine National Reserve and Kisite-Mpunguti Marine National Park; important coastal reef habitat.', 'Diani developed as a beach tourism destination from the 1970s; the Diani-Chale Marine National Reserve was established in 1995.', 'Swimming; snorkelling; diving; kitesurfing; dhow cruises; marine park trips; beach walks.', 'Beach lovers, divers, families, honeymooners, water-sports enthusiasts', 'Half-day to full-day', 'easy', 'November–March for calm seas and visibility; April–October for kitesurfing winds.', 'Public beach access is free. Diani-Chale Marine National Park KWS fee: non-resident adult USD 25/day, child USD 15; East African citizen adult KES 500, child 250. Resort day passes and activities vary.', 'Public beach accessible 24/7; marine park gates typically daytime. Confirm marine park hours locally.', 'About 30 km south of Mombasa via the Likoni Ferry; Ukunda Airstrip serves domestic flights.', 'Not verified. Beach access and resort/boat accessibility vary; confirm with the operator.', 'Resorts, restaurants, dive centres, water-sports operators, and beach bars.', 'Not verified. Swim within designated areas and follow local operator guidance.', 'KWS Conservation Fee (Oct 2025); Kenya Tourism Board; YTCI field research.', 'YTCI Content Team', now() + interval '6 months', 180, 480, 'free', NULL, 0, 'Public beach access is free. Diani-Chale Marine National Park charges a separate KWS conservation fee (non-resident adult USD 25/day).', 'verified', '{"always_open": true, "note": "Public beach; marine park hours apply separately."}'::jsonb, 'https://upload.wikimedia.org/wikipedia/commons/1/12/Diani_Beach_2018.jpg', 'Wedding-Hendrik', 'CC BY-SA 4.0', 'Wikimedia Commons', 'White sand and turquoise water at Diani Beach.'),
    ('maasai-mara', 'Maasai Mara', 'Narok', '033', 'Maasai Mara', 'Wildlife', ARRAY['Wildlife', 'Conservation', 'Photography', 'Culture'], -1.49, 35.14, 'A 1,510 km² savannah reserve in the Greater Serengeti-Mara ecosystem, known for big cats, the Great Migration, and Maasai cultural landscapes.', 'The Maasai Mara National Reserve is one of Africa''s most iconic wildlife conservation areas and the northernmost section of the Greater Serengeti-Mara Ecosystem, which spans over 25,000 km² across Kenya and Tanzania. Renowned for hosting the Great Wildebeest Migration, the reserve supports exceptional wildlife density including all of the Big Five. It covers 1,510 km² of open grassland savannah with acacia trees, seasonal riverlets, and forested river banks along the Mara, Talek, and Sand Rivers.', 'UNESCO Tentative List for World Heritage Site inscription. Hosts the Great Wildebeest Migration and is an Important Bird Area with 500+ bird species.', 'Originally designated as a wildlife sanctuary in 1948, the Maasai Mara was established as a game reserve in 1961. The Maasai people have lived in the area for centuries and continue to practise their traditional pastoralist lifestyle alongside wildlife conservation.', 'Game drives; hot air balloon safaris; guided walking safaris; Maasai cultural village visits; birdwatching; photography; bush breakfasts and sundowners.', 'Wildlife enthusiasts, photographers, families, cultural travellers', 'Full day to multi-day', 'easy', 'July–October for the Great Migration; year-round wildlife viewing.', 'Narok County 2025/26: non-resident adult USD 100/day Jan–Jun, USD 200/day Jul–Dec; child 9–17 USD 50/day; under 8 free. East African resident adult KES 1,000 low / 2,000 high; citizen adult KES 1,500 low / 3,000 high.', 'Park hours not independently verified; confirm with Narok County/KAPS.', 'About 270 km from Nairobi via Narok; scheduled flights from Wilson Airport to Mara airstrips.', 'Not verified. Gravel roads and 4x4 terrain; lodge accessibility varies.', 'Lodges, tented camps, visitor points, and airstrips.', 'Not verified. Stay in the vehicle during game drives and follow guide instructions.', 'Narok County 2025/26 Park Fees & Charges; Kenya Tourism Board; YTCI field research.', 'YTCI Content Team', now() + interval '6 months', 480, 1440, 'verified', 'USD', 100, 'Narok County 2025/26: non-resident adult USD 100/day Jan–Jun, USD 200/day Jul–Dec; child 9–17 USD 50/day; under 8 free. East African resident and citizen rates in KES.', 'unverified', NULL, 'https://upload.wikimedia.org/wikipedia/commons/8/85/Safari_vehicles_watching_lion_couple_in_Maasai_Mara%2C_Kenya.jpg', 'Daniel Case', 'CC BY-SA 4.0', 'Wikimedia Commons', 'Safari vehicles watching lions in the Maasai Mara.'),
    ('amboseli', 'Amboseli', 'Kajiado', '034', 'Amboseli', 'Wildlife', ARRAY['Wildlife', 'Conservation', 'Photography'], -2.6527, 37.2606, 'A savannah national park beneath Mount Kilimanjaro, known for elephant herds, swamps fed by Kilimanjaro meltwater, and open views.', 'Amboseli National Park lies in Kajiado County at the foot of Mount Kilimanjaro. Its open savannah, seasonal swamps, and woodlands support large elephant herds and a wide range of wildlife. The park is one of Kenya''s premium wildlife destinations and offers some of the best views of Kilimanjaro.', 'UNESCO biosphere reserve; a key elephant research and conservation landscape.', 'Established as a national park in 1974; long-term elephant research has been based in the ecosystem since the 1970s.', 'Game drives; elephant viewing; Kilimanjaro viewpoints; birdwatching; photography; Maasai cultural visits.', 'Wildlife enthusiasts, photographers, families, birders', 'Full day to multi-day', 'easy', 'Dry seasons for wildlife concentration; clear Kilimanjaro views are more likely in the morning.', 'KWS Amboseli National Park: non-resident adult USD 90/day, child/student USD 45; East African citizen adult KES 1,500, child 750; Kenya resident adult KES 2,025, child 1,050.', 'Park gate hours not independently verified; confirm with KWS.', 'About 240 km from Nairobi via Emali; airstrip at Amboseli serves charter flights.', 'Not verified. Park roads and lodges vary; confirm accessible options.', 'Lodges, camps, picnic sites, and airstrip.', 'Not verified. Follow KWS rules and guide instructions.', 'KWS Conservation Fee (Oct 2025); UNESCO; YTCI field research.', 'YTCI Content Team', now() + interval '6 months', 480, 1440, 'verified', 'USD', 90, 'KWS Amboseli National Park adult conservation fee: USD 90/day non-resident; child/student USD 45. East African citizen adult KES 1,500; Kenya resident adult KES 2,025. Per person per day.', 'unverified', NULL, 'https://upload.wikimedia.org/wikipedia/commons/e/e1/Amboseli_National_Park_and_Mt._Kilimanjaro.jpg', 'Ninaras', 'CC BY 4.0', 'Wikimedia Commons', 'Elephants and Mount Kilimanjaro in Amboseli National Park.'),
    ('lake-nakuru', 'Lake Nakuru', 'Nakuru', '032', 'Lake Nakuru National Park', 'Wildlife', ARRAY['Wildlife', 'Conservation', 'Photography'], -0.3667, 36.0833, 'An alkaline Rift Valley lake ringed by acacia woodland and a national park, known for flamingos, rhinos, and pelicans.', 'Lake Nakuru is a shallow alkaline lake in the Rift Valley, surrounded by Lake Nakuru National Park. The park protects acacia woodland, grassland, and a rhino sanctuary, and the lake is an important bird area known for flamingos and pelicans. The lake is part of the Kenya Lake System in the Great Rift Valley UNESCO World Heritage Site.', 'Part of the Kenya Lake System in the Great Rift Valley UNESCO World Heritage Site; an Important Bird Area and rhino sanctuary.', 'Lake Nakuru National Park was established in 1961 and expanded in the 1970s; it is one of Kenya''s oldest national parks.', 'Game drives; rhino viewing; birdwatching; viewpoints; photography; picnic sites.', 'Wildlife watchers, birders, photographers, families', '3–6 hours', 'easy', 'Wildlife viewing year-round; water levels and flamingo numbers vary with rainfall.', 'KWS Lake Nakuru National Park: non-resident adult USD 90/day, child/student USD 45; East African citizen adult KES 1,500, child 750; Kenya resident adult KES 2,025, child 1,050.', 'Park gate hours not independently verified; confirm with KWS.', 'About 160 km from Nairobi via Nakuru town; the park gate is close to Nakuru.', 'Not verified. Park roads and viewpoints vary; confirm accessible options.', 'Picnic sites, viewpoints, lodges outside the park, and a rhino sanctuary.', 'Not verified. Follow KWS rules and guide instructions.', 'KWS Conservation Fee (Oct 2025); UNESCO; YTCI field research.', 'YTCI Content Team', now() + interval '6 months', 180, 360, 'verified', 'USD', 90, 'KWS Lake Nakuru National Park adult conservation fee: USD 90/day non-resident; child/student USD 45. East African citizen adult KES 1,500; Kenya resident adult KES 2,025. Per person per day.', 'unverified', NULL, 'https://upload.wikimedia.org/wikipedia/commons/7/70/Lake_Nakuru_National_Park_02_-_Lesser_Flamingo_%28Phoeniconaias_minor%29.jpg', 'Thomas Fuhrmann', 'CC BY-SA 4.0', 'Wikimedia Commons', 'Lesser flamingos at Lake Nakuru National Park.'),
    ('karura-forest', 'Karura Forest', 'Nairobi', '047', 'Nairobi', 'Wildlife', ARRAY['Wildlife', 'Hiking', 'Conservation', 'Photography'], -1.2333, 36.8333, 'A 1,000-acre urban forest in northern Nairobi with indigenous woodland, waterfalls, caves, and marked walking and cycling trails.', 'Karura Forest is a protected urban forest in northern Nairobi, jointly managed by the Kenya Forest Service and the Friends of Karura Forest. It covers over a thousand acres of indigenous and plantation woodland, with waterfalls, caves, and a network of walking, running, and cycling trails. The forest is a popular recreation area and an important green lung for the city.', 'One of Nairobi''s largest urban forests and a community-managed recreation and conservation area.', 'The forest was gazetted as a protected forest; the Friends of Karura Forest was founded in 2009 and jointly manages it with KFS.', 'Walk or run the colour-coded trails; cycle; visit the waterfalls and caves; picnic; birdwatch; join an eco-tour.', 'Walkers, runners, cyclists, families, birders, photographers', '2–5 hours', 'easy', 'Year-round; trails can be muddy after rain.', 'Karura Forest daily entry (w.e.f. 29 Aug 2025): adult KES 116 citizen / KES 232 EAC resident / KES 696 non-resident; child KES 58 / 116 / 348. Parking and activities extra.', 'Open daily 6:00 AM–6:00 PM; last entry ticket 5:30 PM; gates close 7:00 PM.', 'About 6 km from Nairobi city centre; main gate A is off Limuru Road. Matatu routes 11B, 106, 107, and 116 serve the area.', 'Not verified. Trails are uneven; confirm accessible routes with FKF.', 'Gates, parking, toilets, River Café, picnic sites, bike hire, and tennis courts.', 'Not verified. Stay on marked trails and follow forest rules.', 'Friends of Karura Forest entry fees and hours (2025/2026); Kenya Forest Service.', 'YTCI Content Team', now() + interval '6 months', 120, 300, 'verified', 'KES', 116, 'Daily entry: adult KES 116 citizen, KES 232 East African resident, KES 696 non-resident; child KES 58 / 116 / 348. Parking and activities extra. Payment by M-Pesa/eCitizen.', 'verified', '{"daily": "06:00-18:00", "last_entry": "17:30", "gates_close": "19:00"}'::jsonb, 'https://upload.wikimedia.org/wikipedia/commons/a/a8/Forested_Section_within_Karura_Forest_%281%29.jpg', 'Eva Ayub', 'CC BY-SA 4.0', 'Wikimedia Commons', 'Forested trail inside Karura Forest, Nairobi.');

-- Preserve the Google-linked Karura Gate A row when the canonical row is absent.
UPDATE destinations
SET slug = 'karura-forest', name = 'Karura Forest', updated_at = now()
WHERE slug = 'karura-forest-gate-a'
  AND NOT EXISTS (SELECT 1 FROM destinations WHERE slug = 'karura-forest');

-- Archive uncurated alias/duplicate rows in the same county.
CREATE TEMP TABLE golden_aliases (
    slug TEXT NOT NULL,
    county_code TEXT NOT NULL,
    pattern TEXT NOT NULL
);
INSERT INTO golden_aliases (slug, county_code, pattern) VALUES
    ('hells-kitchen', '003', 'hellskitchen%'),
    ('hells-kitchen', '003', 'marafahellskitchen%'),
    ('hells-kitchen', '003', 'marafadepression%'),
    ('chalbi-desert', '010', 'chalbi%'),
    ('lake-paradise', '010', 'lakeparadise%'),
    ('lake-turkana', '023', 'laketurkana%'),
    ('fort-jesus', '001', 'fortjesus%'),
    ('diani-beach', '002', 'dianibeach%'),
    ('maasai-mara', '033', 'maasaimara%'),
    ('maasai-mara', '033', 'masaimara%'),
    ('amboseli', '034', 'amboseli%'),
    ('lake-nakuru', '032', 'lakenakuru%'),
    ('karura-forest', '047', 'karuraforest%');

UPDATE destinations d
SET status = 'archived', updated_at = now()
FROM golden_aliases a
WHERE d.county_code = a.county_code
  AND d.slug <> a.slug
  AND d.is_curated = false
  AND regexp_replace(lower(d.name), '[^a-z0-9]', '', 'g') LIKE a.pattern;

INSERT INTO destinations (
    slug, name, county, county_code, locality, category, interest_tags, status,
    is_curated, location, short_description, full_description, significance,
    history, things_to_do, suitable_audiences, duration, difficulty, seasonality,
    indicative_fees, opening_info, transport_notes, accessibility, facilities,
    safety_notes, source, content_owner, verification_status, review_date,
    visit_duration_min_minutes, visit_duration_max_minutes, fee_status,
    fee_currency, fee_amount, fee_basis, opening_hours_status, opening_hours,
    hero_image_url, hero_image_attribution, hero_image_license, hero_image_source,
    hero_image_alt
)
SELECT
    g.slug, g.name, g.county, g.county_code, g.locality, g.category,
    g.interest_tags, 'published', true,
    ST_SetSRID(ST_MakePoint(g.longitude, g.latitude), 4326),
    g.short_description, g.full_description, g.significance, g.history,
    g.things_to_do, g.suitable_audiences, g.duration, g.difficulty, g.seasonality,
    g.indicative_fees, g.opening_info, g.transport_notes, g.accessibility,
    g.facilities, g.safety_notes, g.source, g.content_owner, 'verified',
    g.review_date, g.visit_duration_min_minutes, g.visit_duration_max_minutes,
    g.fee_status, g.fee_currency, g.fee_amount, g.fee_basis,
    g.opening_hours_status, g.opening_hours, g.hero_image_url,
    g.hero_image_attribution, g.hero_image_license, g.hero_image_source,
    g.hero_image_alt
FROM golden_destinations g
ON CONFLICT (slug) DO UPDATE SET
    name = EXCLUDED.name,
    county = EXCLUDED.county,
    county_code = EXCLUDED.county_code,
    locality = EXCLUDED.locality,
    category = EXCLUDED.category,
    interest_tags = EXCLUDED.interest_tags,
    status = EXCLUDED.status,
    is_curated = EXCLUDED.is_curated,
    location = EXCLUDED.location,
    short_description = EXCLUDED.short_description,
    full_description = EXCLUDED.full_description,
    significance = EXCLUDED.significance,
    history = EXCLUDED.history,
    things_to_do = EXCLUDED.things_to_do,
    suitable_audiences = EXCLUDED.suitable_audiences,
    duration = EXCLUDED.duration,
    difficulty = EXCLUDED.difficulty,
    seasonality = EXCLUDED.seasonality,
    indicative_fees = EXCLUDED.indicative_fees,
    opening_info = EXCLUDED.opening_info,
    transport_notes = EXCLUDED.transport_notes,
    accessibility = EXCLUDED.accessibility,
    facilities = EXCLUDED.facilities,
    safety_notes = EXCLUDED.safety_notes,
    source = EXCLUDED.source,
    content_owner = EXCLUDED.content_owner,
    verification_status = EXCLUDED.verification_status,
    review_date = EXCLUDED.review_date,
    verified_at = now(),
    visit_duration_min_minutes = EXCLUDED.visit_duration_min_minutes,
    visit_duration_max_minutes = EXCLUDED.visit_duration_max_minutes,
    fee_status = EXCLUDED.fee_status,
    fee_currency = EXCLUDED.fee_currency,
    fee_amount = EXCLUDED.fee_amount,
    fee_basis = EXCLUDED.fee_basis,
    opening_hours_status = EXCLUDED.opening_hours_status,
    opening_hours = EXCLUDED.opening_hours,
    hero_image_url = EXCLUDED.hero_image_url,
    hero_image_attribution = EXCLUDED.hero_image_attribution,
    hero_image_license = EXCLUDED.hero_image_license,
    hero_image_source = EXCLUDED.hero_image_source,
    hero_image_alt = EXCLUDED.hero_image_alt,
    updated_at = now();

-- Structured transport/season filters for the composer.
UPDATE destinations d
SET transport_modes = v.transport_modes,
    best_seasons = v.best_seasons,
    updated_at = now()
FROM (VALUES
    ('hells-kitchen', ARRAY['car', 'walking']::TEXT[], ARRAY['dry']::TEXT[]),
    ('chalbi-desert', ARRAY['4x4']::TEXT[], ARRAY['dry']::TEXT[]),
    ('lake-paradise', ARRAY['car', '4x4']::TEXT[], ARRAY['dry']::TEXT[]),
    ('lake-turkana', ARRAY['car', '4x4', 'flight', 'boat']::TEXT[], ARRAY['dry']::TEXT[]),
    ('fort-jesus', ARRAY['car', 'public_transport', 'walking']::TEXT[], ARRAY['year_round']::TEXT[]),
    ('diani-beach', ARRAY['car', 'public_transport', 'flight', 'boat']::TEXT[], ARRAY['year_round']::TEXT[]),
    ('maasai-mara', ARRAY['car', '4x4', 'flight']::TEXT[], ARRAY['dry', 'migration']::TEXT[]),
    ('amboseli', ARRAY['car', '4x4', 'flight']::TEXT[], ARRAY['dry', 'year_round']::TEXT[]),
    ('lake-nakuru', ARRAY['car', 'public_transport']::TEXT[], ARRAY['year_round']::TEXT[]),
    ('karura-forest', ARRAY['car', 'public_transport', 'walking', 'cycling']::TEXT[], ARRAY['year_round']::TEXT[])
) AS v(slug, transport_modes, best_seasons)
WHERE d.slug = v.slug;

DO $$
DECLARE
    fixture_count INTEGER;
    county_count INTEGER;
BEGIN
    SELECT count(*) INTO fixture_count
    FROM golden_destinations g
    JOIN destinations d ON d.slug = g.slug
    WHERE d.status = 'published'
      AND d.is_curated
      AND d.verification_status = 'verified'
      AND d.county_code = g.county_code;

    IF fixture_count <> 10 THEN
        RAISE EXCEPTION 'golden fixtures: expected 10 published verified rows, found %', fixture_count;
    END IF;

    SELECT count(DISTINCT county_code) INTO county_count
    FROM destinations
    WHERE is_curated AND status = 'published';

    IF county_count < 9 THEN
        RAISE EXCEPTION 'golden fixtures: expected at least 9 curated counties, found %', county_count;
    END IF;
END $$;

COMMIT;
