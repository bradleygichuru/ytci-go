-- Big Five seed for internal testing. Idempotent by slug.
BEGIN;

INSERT INTO content_languages (tag, english_name, native_name, coverage_tier, writing_direction, launch_phase, status)
VALUES ('en','English','English','full','ltr',1,'active'),
       ('sw','Swahili','Kiswahili','full','ltr',1,'active')
ON CONFLICT (tag) DO NOTHING;

INSERT INTO species (slug, scientific_name, authorship, taxonomic_rank, taxonomic_status, iucn_category, iucn_assessment_id, red_list_version, status_as_of, content_version, publication_state)
VALUES
 ('lion','Panthera leo','Linnaeus, 1758','species','accepted','VU','IUCN-2023-lion','3.1','2023-06-30',1,'published'),
 ('african-elephant','Loxodonta africana','Blumenbach, 1797','species','accepted','EN','IUCN-2021-elephant','3.1','2021-03-25',1,'published'),
 ('african-buffalo','Syncerus caffer','Sparrman, 1779','species','accepted','NT','IUCN-2018-buffalo','3.1','2018-08-08',1,'published'),
 ('african-leopard','Panthera pardus','Linnaeus, 1758','species','accepted','VU','IUCN-2019-leopard','3.1','2019-03-15',1,'published'),
 ('black-rhinoceros','Diceros bicornis','Linnaeus, 1758','species','accepted','CR','IUCN-2020-rhino','3.1','2020-03-26',1,'published')
ON CONFLICT (slug) DO UPDATE SET publication_state='published', updated_at=now();

-- Names
INSERT INTO species_names (species_id, language_tag, script, name, name_type, review_status, preferred)
SELECT s.id, v.lang, v.script, v.name, 'common', 'reviewed', true
FROM species s
JOIN (VALUES
 ('lion','en','Latn','Lion'), ('lion','sw','Latn','Simba'),
 ('african-elephant','en','Latn','African bush elephant'), ('african-elephant','sw','Latn','Tembo'),
 ('african-buffalo','en','Latn','African buffalo'), ('african-buffalo','sw','Latn','Nyati'),
 ('african-leopard','en','Latn','African leopard'), ('african-leopard','sw','Latn','Chui'),
 ('black-rhinoceros','en','Latn','Black rhinoceros'), ('black-rhinoceros','sw','Latn','Kifaru')
) AS v(slug, lang, script, name) ON v.slug = s.slug
WHERE NOT EXISTS (
  SELECT 1 FROM species_names n WHERE n.species_id = s.id AND n.language_tag = v.lang AND n.name = v.name
);

-- Content (EN + SW)
INSERT INTO species_content (species_id, language_tag, coverage_tier, summary, description, habitat, range_text, where_to_see_text, conservation_text, field_status, source_version, review_status, published_at)
SELECT s.id, v.lang, 'full', v.summary, v.description, v.habitat, v.range_text, v.where_text, v.conservation, '{}'::jsonb, 1, 'reviewed', now()
FROM species s
JOIN (VALUES
 ('lion','en',
  'The lion is Africa''s largest social cat, living in prides across savanna and grassland.',
  'Lions hunt cooperatively, usually at dawn and dusk. Males defend the pride territory; females do most of the hunting. A pride''s roar carries for kilometres.',
  'Open savanna, grasslands and light woodland with sufficient prey and cover.',
  'Across sub-Saharan Africa, with strong populations in Kenya''s Maasai Mara and Tsavo ecosystems.',
  'Maasai Mara National Reserve, Tsavo East and West, Amboseli.',
  'Vulnerable. Habitat loss, human-wildlife conflict and declining prey are the main pressures.'),
 ('lion','sw',
  'Simba ni paka mkubwa zaidi wa Afrika anayeishi katika makundi kwenye savana.',
  'Simba huwinda kwa ushirikiano, mara nyingi alfajiri na jioni. Simba wa kike hufanya sehemu kubwa ya uwindaji.',
  'Savana, nyasi na misitu hafifu yenye mawindo ya kutosha.',
  'Afrika kusini mwa Sahara, ikiwa na idadi kubwa katika Hifadhi ya Maasai Mara na Tsavo nchini Kenya.',
  'Hifadhi ya Maasai Mara, Tsavo Mashariki na Magharibi, Amboseli.',
  'Wako hatarini. Upotevu wa makazi na migogoro ya binadamu na wanyamapori ndio vitisho vikuu.'),
 ('african-elephant','en',
  'The African bush elephant is the largest living land animal and a skilled ecosystem engineer.',
  'Elephants live in matriarchal herds, communicate over long distances and shape savanna by dispersing seeds and opening waterholes.',
  'Savanna, bushland and forest edges with reliable water.',
  'Widely distributed across sub-Saharan Africa; Kenya''s strongholds include Tsavo, Amboseli and Laikipia.',
  'Tsavo East and West, Amboseli, Samburu, Laikipia.',
  'Endangered. Poaching for ivory and habitat fragmentation remain the main threats.'),
 ('african-elephant','sw',
  'Tembo wa Afrika ni mnyama mkubwa zaidi wa nchi kavu na mhandisi muhimu wa mazingira.',
  'Tembo huishi katika makundi yanayoongozwa na jike mkuu na husafirisha mbegu na kufungua visima vya maji.',
  'Savana, misitu hafifu na kingo za misitu yenye maji ya kutosha.',
  'Wameenea Afrika kusini mwa Sahara; maeneo muhimu Kenya ni Tsavo, Amboseli na Laikipia.',
  'Tsavo Mashariki na Magharibi, Amboseli, Samburu, Laikipia.',
  'Wako hatarini sana. Ujangili wa pembe na mgawanyiko wa makazi ni vitisho vikuu.'),
 ('african-buffalo','en',
  'The African buffalo is a powerful herd-dwelling grazer of savanna and wetland edges.',
  'Buffalo herds move with water and grazing, defended collectively against predators. Old males are often solitary and notoriously unpredictable.',
  'Savanna, floodplain and reedbeds near permanent water.',
  'Sub-Saharan Africa; common in Kenya''s Mara, Tsavo and Amboseli ecosystems.',
  'Maasai Mara, Tsavo, Amboseli, Lake Nakuru.',
  'Near Threatened. Disease, water loss and competition with livestock are the main pressures.'),
 ('african-buffalo','sw',
  'Nyati ni mnyama mwenye nguvu anayeishi kwa makundi na kula nyasi kwenye savana.',
  'Makundi ya nyati hufuata maji na malisho, na hujilinda kwa pamoja dhidi ya wawindaji.',
  'Savana, mabwawa na nyasi karibu na maji ya kudumu.',
  'Afrika kusini mwa Sahara; wanapatikana Mara, Tsavo na Amboseli nchini Kenya.',
  'Maasai Mara, Tsavo, Amboseli, Ziwa Nakuru.',
  'Wako karibu kutoweka. Magonjwa, upungufu wa maji na ushindani na mifugo ni vitisho.'),
 ('african-leopard','en',
  'The African leopard is a solitary, adaptable big cat and a master of stealth.',
  'Leopards are nocturnal ambush hunters that haul prey into trees. They tolerate a wide range of habitats and are rarely seen.',
  'Savanna, riverine forest, rocky hills and forest margins.',
  'Widely distributed across sub-Saharan Africa, including Kenya''s Mara, Laikipia and Tsavo.',
  'Maasai Mara, Laikipia, Tsavo, Nairobi National Park.',
  'Vulnerable. Habitat loss, retaliatory killing and prey depletion are the main threats.'),
 ('african-leopard','sw',
  'Chui ni paka mkubwa anayeishi peke yake na mwindaji hodari wa kujificha.',
  'Chui huwinda usiku na kupandisha mawindo mitini. Huishi katika mazingira mbalimbali na ni vigumu kuwaona.',
  'Savana, misitu ya mito, vilima vya mawe na pembezoni mwa misitu.',
  'Wameenea Afrika kusini mwa Sahara, ikiwa ni pamoja na Mara, Laikipia na Tsavo nchini Kenya.',
  'Maasai Mara, Laikipia, Tsavo, Hifadhi ya Taifa ya Nairobi.',
  'Wako hatarini. Upotevu wa makazi, mauaji ya kulipiza kisasi na upungufu wa mawindo ni vitisho.'),
 ('black-rhinoceros','en',
  'The black rhinoceros is a critically endangered browser with a hooked lip adapted to shrubs.',
  'Black rhinos are solitary and browse on bushes and shrubs. They rely on smell and hearing more than sight and can be surprisingly fast.',
  'Dry bushland, thicket and savanna with dense shrubs.',
  'Kenya holds key populations in Nairobi National Park, Tsavo and Laikipia; historic range once spanned much of sub-Saharan Africa.',
  'Nairobi National Park, Tsavo, Laikipia, Maasai Mara.',
  'Critically Endangered. Poaching for horn remains the dominant threat, despite intensive protection.'),
 ('black-rhinoceros','sw',
  'Kifaru mweusi ni mnyama aliyeko hatarini sana anayekula majani ya vichaka kwa mdomo uliopinda.',
  'Kifaru mweusi huishi peke yake na hutegemea harufu na usikivu zaidi ya uwezo wa kuona.',
  'Maeneo makavu ya vichaka, savana na misitu yenye miti mizito.',
  'Kenya ina idadi muhimu katika Hifadhi ya Taifa ya Nairobi, Tsavo na Laikipia.',
  'Hifadhi ya Taifa ya Nairobi, Tsavo, Laikipia, Maasai Mara.',
  'Aliyeko hatarini sana. Ujangili wa pembe bado ni tishio kubwa licha ya ulinzi mkali.')
) AS v(slug, lang, summary, description, habitat, range_text, where_text, conservation) ON v.slug = s.slug
WHERE NOT EXISTS (
  SELECT 1 FROM species_content c WHERE c.species_id = s.id AND c.language_tag = v.lang
);

-- Media (hero image in R2)
INSERT INTO species_media (species_id, media_kind, storage_key, rights_holder, credit, licence_code, licence_url, sensitive_location, review_status, published_at)
SELECT s.id, 'image', 'species/big-five/' || s.slug || '.jpg', 'Wikimedia Commons contributors', 'Wikimedia Commons', 'CC BY-SA 4.0', 'https://creativecommons.org/licenses/by-sa/4.0/', false, 'reviewed', now()
FROM species s
WHERE s.slug IN ('lion','african-elephant','african-buffalo','african-leopard','black-rhinoceros')
  AND NOT EXISTS (SELECT 1 FROM species_media m WHERE m.species_id = s.id AND m.storage_key = 'species/big-five/' || s.slug || '.jpg');

-- Where to see it
INSERT INTO species_where_to_see (species_id, protected_area, evidence_type, confidence, review_status, notes)
SELECT s.id, v.area, 'published_report', 'high', 'reviewed', v.notes
FROM species s
JOIN (VALUES
 ('lion','Maasai Mara National Reserve','Resident prides and high sighting rates.'),
 ('lion','Tsavo East National Park','Wide savanna with resident prides.'),
 ('african-elephant','Amboseli National Park','Iconic herds with long-term research.'),
 ('african-elephant','Tsavo East National Park','Large free-ranging population.'),
 ('african-buffalo','Maasai Mara National Reserve','Large herds follow the Mara grasslands.'),
 ('african-buffalo','Lake Nakuru National Park','Resident herd around the lake.'),
 ('african-leopard','Maasai Mara National Reserve','Reliable sightings in riverine forest.'),
 ('african-leopard','Laikipia','Healthy population on private conservancies.'),
 ('black-rhinoceros','Nairobi National Park','Protected population close to the city.'),
 ('black-rhinoceros','Laikipia','Key sanctuary populations.')
) AS v(slug, area, notes) ON v.slug = s.slug
WHERE NOT EXISTS (
  SELECT 1 FROM species_where_to_see w WHERE w.species_id = s.id AND w.protected_area = v.area
);

COMMIT;
