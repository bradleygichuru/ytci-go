-- MICE production seed for public testing.
--
-- Scope: venues, organizer profiles and staff, events, event/venue
-- accessibility, agenda sessions, speakers, registrations (including a
-- waitlisted attendee and access needs), ticket handoffs, tickets and check-ins.
--
-- Idempotent by natural key:
--   * venues and organizer_profiles upsert on slug
--   * events, sessions, people and staff upsert on their fixed seed id
--   * registrations upsert on (event_id, user_id)
--   * tickets, check-ins and handoffs upsert on their unique key
--
-- The script never deletes rows and never touches events it did not create.
-- It references existing user accounts by email; a missing account only skips
-- that user's staff/registration/handoff row instead of failing the seed.
--
-- Apply with:
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -1 -f scripts/seed/mice-production.sql
--
-- Schema note: the deployed MICE schema models one price per event
-- (price_amount/price_currency/ticket_url/sales_end). There is no ticket-tier
-- table, so paid events carry their tier copy in the description and use the
-- single organizer handoff price. See scripts/seed/README.md.

BEGIN;

-- ---------------------------------------------------------------------------
-- Venues
-- ---------------------------------------------------------------------------

INSERT INTO venues (
    slug, name, address_line1, county, county_gid, latitude, longitude,
    capacity_in_person, rooms, contact_email, contact_phone,
    transport_notes, dropoff_notes, parking_notes, verified_at, created_by
)
VALUES
    (
        'kicc-nairobi',
        'Kenyatta International Convention Centre',
        'P.O. Box 30746, Harambee Avenue, Nairobi',
        'Nairobi', 47, -1.29210000, 36.82190000,
        5000,
        '["Lenana Hall","Tsavo Ballroom","Aberdare Room"]'::jsonb,
        'events@kicc.co.ke', '+254 20 326 1000',
        'Walking distance from Nairobi CBD hotels; secure taxi drop-off at the Harambee Avenue gate.',
        'Use the main Harambee Avenue gate. Coaches use the Parliament Road lay-by.',
        'Paid basement parking for 220 cars; two accessible bays beside the lift lobby.',
        '2026-10-15 09:00:00+03',
        (SELECT id FROM users WHERE email = 'grace@example.com')
    ),
    (
        'sarova-whitesands-mombasa',
        'Sarova Whitesands Beach Resort and Spa',
        'Mombasa-Malindi Road, Bamburi, Mombasa',
        'Mombasa', 1, -4.03330000, 39.71670000,
        1200,
        '["Bahari Ballroom","Coral Room","Reef Boardroom"]'::jsonb,
        'events@whitesands.co.ke', '+254 41 212 8000',
        'Twenty minutes from Moi International Airport; airport transfers can be arranged with the venue.',
        'Coaches drop at the conference wing entrance, not the hotel lobby.',
        'Complimentary guest parking; overflow parking at the Bamburi beach access lot.',
        '2026-10-20 09:00:00+03',
        (SELECT id FROM users WHERE email = 'grace@example.com')
    ),
    (
        'mara-serena-narok',
        'Mara Serena Safari Lodge Conference Centre',
        'Mara Triangle, Maasai Mara, Narok',
        'Narok', 33, -1.41670000, 35.01670000,
        300,
        '["Mara Conference Centre","Oloololo Room"]'::jsonb,
        'mice@serenahotels.co.ke', '+254 20 284 2000',
        'Accessible by 4x4 or light aircraft to Mara Serena airstrip; road transfer from Narok town takes about three hours.',
        'Vehicle drop-off is at the conference centre ramp; the lodge runs a shuttle from the airstrip.',
        'Gravel parking for 40 vehicles beside the conference wing.',
        '2026-10-25 09:00:00+03',
        (SELECT id FROM users WHERE email = 'grace@example.com')
    )
ON CONFLICT (slug) DO UPDATE SET
    name = EXCLUDED.name,
    address_line1 = EXCLUDED.address_line1,
    county = EXCLUDED.county,
    county_gid = EXCLUDED.county_gid,
    latitude = EXCLUDED.latitude,
    longitude = EXCLUDED.longitude,
    capacity_in_person = EXCLUDED.capacity_in_person,
    rooms = EXCLUDED.rooms,
    contact_email = EXCLUDED.contact_email,
    contact_phone = EXCLUDED.contact_phone,
    transport_notes = EXCLUDED.transport_notes,
    dropoff_notes = EXCLUDED.dropoff_notes,
    parking_notes = EXCLUDED.parking_notes,
    verified_at = EXCLUDED.verified_at,
    updated_at = now();

INSERT INTO venue_accessibility (
    venue_id, step_free_entrance, step_free_route, accessible_toilet,
    accessible_parking, accessible_seating, captioning, sign_language,
    hearing_loop, quiet_room, assistance_dogs, companion_ticket_policy,
    content_notes, reported_by, verified_at
)
SELECT v.id, a.step_free_entrance, a.step_free_route, a.accessible_toilet,
       a.accessible_parking, a.accessible_seating, a.captioning, a.sign_language,
       a.hearing_loop, a.quiet_room, a.assistance_dogs, a.companion_ticket_policy,
       a.content_notes, a.reported_by, a.verified_at
FROM (
    VALUES
        (
            'kicc-nairobi', 'yes', 'yes', 'yes', 'yes', 'yes', 'yes', 'no',
            'yes', 'yes', 'yes',
            'One companion ticket is issued free with every accessible-seat booking; request it at checkout or by email.',
            'The quiet room is on level 2 beside the Lenana Hall foyer. Wheelchairs are available from the main reception.',
            'Nairobi Convention Bureau', '2026-10-15 09:00:00+03'::timestamptz
        ),
        (
            'sarova-whitesands-mombasa', 'yes', 'yes', 'yes', 'yes', 'yes', 'no', 'no',
            'unknown', 'yes', 'yes',
            'Companion tickets are charged at the delegate rate; contact the events desk for a refundable companion pass.',
            'The beach path is sand and is not step-free. The conference wing, pool deck and restaurants have step-free routes.',
            'Coast Hospitality Association', '2026-10-20 09:00:00+03'::timestamptz
        ),
        (
            'mara-serena-narok', 'yes', 'unknown', 'yes', 'no', 'yes', 'no', 'no',
            'no', 'yes', 'yes',
            'Companion tickets are free for personal assistants; state the requirement when registering.',
            'The conference centre is step-free, but some garden paths are gravel. Game drives use adapted vehicles on request.',
            'Maasai Mara Tourism Alliance', '2026-10-25 09:00:00+03'::timestamptz
        )
) AS a(slug, step_free_entrance, step_free_route, accessible_toilet,
       accessible_parking, accessible_seating, captioning, sign_language,
       hearing_loop, quiet_room, assistance_dogs, companion_ticket_policy,
       content_notes, reported_by, verified_at)
JOIN venues v ON v.slug = a.slug
ON CONFLICT (venue_id) DO UPDATE SET
    step_free_entrance = EXCLUDED.step_free_entrance,
    step_free_route = EXCLUDED.step_free_route,
    accessible_toilet = EXCLUDED.accessible_toilet,
    accessible_parking = EXCLUDED.accessible_parking,
    accessible_seating = EXCLUDED.accessible_seating,
    captioning = EXCLUDED.captioning,
    sign_language = EXCLUDED.sign_language,
    hearing_loop = EXCLUDED.hearing_loop,
    quiet_room = EXCLUDED.quiet_room,
    assistance_dogs = EXCLUDED.assistance_dogs,
    companion_ticket_policy = EXCLUDED.companion_ticket_policy,
    content_notes = EXCLUDED.content_notes,
    reported_by = EXCLUDED.reported_by,
    verified_at = EXCLUDED.verified_at,
    updated_at = now();

-- ---------------------------------------------------------------------------
-- Organizers and staff
-- ---------------------------------------------------------------------------

INSERT INTO organizer_profiles (
    slug, display_name, trust_tier, verification_status, about, logo_url,
    contact_email, contact_phone, website_url, legal_name, verified_at,
    verification_expires_at, created_by
)
VALUES
    (
        'nairobi-convention-bureau',
        'Nairobi Convention Bureau',
        'programme_partner', 'verified',
        'The Nairobi Convention Bureau markets the city as a meetings, incentives, conferences and exhibitions destination and supports association bids with venue, access and delegate-service advice.',
        'https://picsum.photos/seed/ytci-ncb/240/240',
        'mice@ncb.co.ke', '+254 20 800 1000', 'https://ncb.co.ke',
        'Nairobi Convention Bureau Limited',
        '2026-01-15 09:00:00+03', '2027-01-15 09:00:00+03',
        (SELECT id FROM users WHERE email = 'grace@example.com')
    ),
    (
        'coast-hospitality-association',
        'Coast Hospitality Association',
        'verified_organizer', 'verified',
        'A membership body for hotels, venues and event suppliers on the Kenyan coast. It runs the annual coastal hospitality forum and maintains shared accessibility guidance for member venues.',
        'https://picsum.photos/seed/ytci-cha/240/240',
        'events@coasthospitality.co.ke', '+254 41 222 4500', 'https://coasthospitality.co.ke',
        'Coast Hospitality Association',
        '2026-02-01 09:00:00+03', '2027-02-01 09:00:00+03',
        (SELECT id FROM users WHERE email = 'grace@example.com')
    ),
    (
        'maasai-mara-tourism-alliance',
        'Maasai Mara Tourism Alliance',
        'verified_organizer', 'verified',
        'A county-level alliance of camps, guides and conservancies working on community-owned tourism, cultural programming and low-impact incentive travel in Narok.',
        'https://picsum.photos/seed/ytci-mmt/240/240',
        'info@maratourismalliance.co.ke', '+254 20 555 0180', 'https://maratourismalliance.co.ke',
        'Maasai Mara Tourism Alliance',
        '2026-03-10 09:00:00+03', '2027-03-10 09:00:00+03',
        (SELECT id FROM users WHERE email = 'grace@example.com')
    )
ON CONFLICT (slug) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    trust_tier = EXCLUDED.trust_tier,
    verification_status = EXCLUDED.verification_status,
    about = EXCLUDED.about,
    logo_url = EXCLUDED.logo_url,
    contact_email = EXCLUDED.contact_email,
    contact_phone = EXCLUDED.contact_phone,
    website_url = EXCLUDED.website_url,
    legal_name = EXCLUDED.legal_name,
    verified_at = EXCLUDED.verified_at,
    verification_expires_at = EXCLUDED.verification_expires_at,
    revoked_at = NULL,
    revoked_reason = NULL,
    updated_at = now();

INSERT INTO organizer_staff (id, organizer_id, user_id, staff_role, assigned_by)
SELECT s.id, o.id, u.id, s.staff_role,
       (SELECT id FROM users WHERE email = 'grace@example.com')
FROM (
    VALUES
        ('e1000000-0000-4000-8000-000000000001'::uuid, 'nairobi-convention-bureau', 'alice@example.com', 'scanner'),
        ('e1000000-0000-4000-8000-000000000002'::uuid, 'nairobi-convention-bureau', 'dave@example.com', 'manager'),
        ('e1000000-0000-4000-8000-000000000003'::uuid, 'coast-hospitality-association', 'bob@example.com', 'scanner'),
        ('e1000000-0000-4000-8000-000000000004'::uuid, 'maasai-mara-tourism-alliance', 'frank@example.com', 'scanner'),
        ('e1000000-0000-4000-8000-000000000005'::uuid, 'maasai-mara-tourism-alliance', 'joanadhiambo7@gmail.com', 'manager')
) AS s(id, organizer_slug, email, staff_role)
JOIN organizer_profiles o ON o.slug = s.organizer_slug
JOIN users u ON u.email = s.email
ON CONFLICT (id) DO UPDATE SET
    organizer_id = EXCLUDED.organizer_id,
    user_id = EXCLUDED.user_id,
    staff_role = EXCLUDED.staff_role,
    assigned_by = EXCLUDED.assigned_by,
    revoked_at = NULL,
    revoked_by = NULL;

-- ---------------------------------------------------------------------------
-- Events
--
-- Lifecycle coverage: draft, published (upcoming, ongoing and ended), cancelled.
-- Free events use RSVP; paid events use organizer ticket handoff.
-- ---------------------------------------------------------------------------

WITH seed (
    id, title, organizer_slug, venue_slug, county, county_gid, event_date,
    end_date, start_time, end_time, theme, status, event_class, format, access,
    capacity, waitlist_capacity, registration_mode, price_amount, sales_end,
    ticket_url, sales_status, published_at, cancelled_at, description,
    contact_email, image_url
) AS (
    VALUES
        (
            'b1000000-0000-4000-8000-000000000001'::uuid,
            'Nairobi Technology and Tourism Summit 2027',
            'nairobi-convention-bureau', 'kicc-nairobi', 'Nairobi', 47,
            '2027-01-28'::date, '2027-01-29'::date, '09:00', '17:00',
            'tourism', 'draft', 'conference', 'hybrid', 'registered',
            300, 60, 'rsvp', NULL::numeric, NULL::timestamptz, NULL::text,
            NULL::text, NULL::timestamptz, NULL::timestamptz,
            'A two-day working summit on travel technology, destination data and digital visitor services. The programme is still in draft while the call for speakers closes.',
            'summit@ncb.co.ke', 'https://picsum.photos/seed/ytci-nairobi-tech-summit/1600/900'
        ),
        (
            'b1000000-0000-4000-8000-000000000002'::uuid,
            'East Africa MICE Exchange 2027',
            'nairobi-convention-bureau', 'kicc-nairobi', 'Nairobi', 47,
            '2027-02-18'::date, '2027-02-19'::date, '08:30', '17:00',
            'tourism', 'published', 'conference', 'in_person', 'public',
            120, 40, 'rsvp', NULL::numeric, NULL::timestamptz, NULL::text,
            NULL::text, '2026-11-05 09:00:00+03'::timestamptz, NULL::timestamptz,
            'The annual buyer and supplier exchange for East African meetings, incentives, conferences and exhibitions. Two days of hosted buyer appointments, association clinics and venue showcases at KICC.',
            'exchange@ncb.co.ke', 'https://picsum.photos/seed/ytci-mice-exchange/1600/900'
        ),
        (
            'b1000000-0000-4000-8000-000000000003'::uuid,
            'Mombasa Coastal Hospitality Forum 2026',
            'coast-hospitality-association', 'sarova-whitesands-mombasa', 'Mombasa', 1,
            '2026-11-23'::date, '2026-11-23'::date, '09:00', '17:00',
            'tourism', 'published', 'meeting', 'in_person', 'registered',
            200, NULL, 'ticket_handoff', 4500.00, '2026-11-22 23:59:00+03'::timestamptz,
            'https://tickets.example.com/mombasa-coastal-forum-2026', 'ended',
            '2026-10-01 09:00:00+03'::timestamptz, NULL::timestamptz,
            'The coastal hospitality sector meets for a one-day forum on seasonality, staffing and accessible guest experiences. Tickets are sold by the organizer: Delegate at KES 4,500, or Delegate plus site visit at KES 6,500. YTCI does not process payment.',
            'forum@coasthospitality.co.ke', 'https://picsum.photos/seed/ytci-mombasa-forum/1600/900'
        ),
        (
            'b1000000-0000-4000-8000-000000000004'::uuid,
            'Mara Safari Incentive Showcase 2027',
            'maasai-mara-tourism-alliance', 'mara-serena-narok', 'Narok', 33,
            '2027-03-05'::date, '2027-03-07'::date, '10:00', '16:00',
            'conservation', 'published', 'incentive', 'in_person', 'registered',
            80, NULL, 'ticket_handoff', 12000.00, '2027-03-01 23:59:00+03'::timestamptz,
            'https://tickets.example.com/mara-incentive-showcase-2027', 'on_sale',
            '2026-11-10 09:00:00+03'::timestamptz, NULL::timestamptz,
            'A three-day hosted showcase for incentive planners, combining camp site inspections, conservation briefings and a community-run cultural programme. Organizer ticket: KES 12,000 per planner, refundable against a confirmed 2027 booking.',
            'showcase@maratourismalliance.co.ke', 'https://picsum.photos/seed/ytci-mara-incentive/1600/900'
        ),
        (
            'b1000000-0000-4000-8000-000000000005'::uuid,
            'Narok Community Tourism Expo 2026',
            'maasai-mara-tourism-alliance', 'mara-serena-narok', 'Narok', 33,
            '2026-09-18'::date, '2026-09-18'::date, '09:00', '18:00',
            'cultural', 'published', 'community', 'in_person', 'public',
            150, 30, 'rsvp', NULL::numeric, NULL::timestamptz, NULL::text,
            NULL::text, '2026-07-01 09:00:00+03'::timestamptz, NULL::timestamptz,
            'A free community open day for Narok tour operators, guides, beadwork cooperatives and homestay hosts. The 2026 edition has ended; the page is kept public as a past-event record.',
            'expo@maratourismalliance.co.ke', 'https://picsum.photos/seed/ytci-narok-expo/1600/900'
        ),
        (
            'b1000000-0000-4000-8000-000000000006'::uuid,
            'Mombasa Marine Conservation Workshop 2026',
            'coast-hospitality-association', 'sarova-whitesands-mombasa', 'Mombasa', 1,
            '2026-10-10'::date, '2026-10-10'::date, '08:00', '13:00',
            'conservation', 'published', 'community', 'hybrid', 'public',
            60, 15, 'rsvp', NULL::numeric, NULL::timestamptz, NULL::text,
            NULL::text, '2026-08-15 09:00:00+03'::timestamptz, NULL::timestamptz,
            'A half-day workshop for beach operators and hotel sustainability leads on reef-safe practice, waste handling and guest communication. The 2026 session has ended.',
            'marine@coasthospitality.co.ke', 'https://picsum.photos/seed/ytci-mombasa-marine/1600/900'
        ),
        (
            'b1000000-0000-4000-8000-000000000007'::uuid,
            'Nairobi East African Craft Exhibition 2027',
            'nairobi-convention-bureau', 'kicc-nairobi', 'Nairobi', 47,
            '2027-03-12'::date, '2027-03-14'::date, '10:00', '19:00',
            'cultural', 'published', 'exhibition', 'in_person', 'public',
            800, NULL, 'ticket_handoff', 1500.00, '2027-03-10 23:59:00+03'::timestamptz,
            'https://tickets.example.com/nairobi-craft-exhibition-2027', 'on_sale',
            '2026-11-12 09:00:00+03'::timestamptz, NULL::timestamptz,
            'A three-day trade and public exhibition of East African craft, design and food producers. Organizer tickets: KES 1,500 for a trade pass; public day tickets are sold at the gate.',
            'craft@ncb.co.ke', 'https://picsum.photos/seed/ytci-nairobi-craft/1600/900'
        ),
        (
            'b1000000-0000-4000-8000-000000000008'::uuid,
            'Lamu Cultural Heritage Festival 2027',
            'coast-hospitality-association', 'sarova-whitesands-mombasa', 'Mombasa', 1,
            '2027-04-10'::date, '2027-04-12'::date, '09:00', '20:00',
            'cultural', 'cancelled', 'community', 'in_person', 'public',
            250, 50, 'rsvp', NULL::numeric, NULL::timestamptz, NULL::text,
            NULL::text, '2026-10-05 09:00:00+03'::timestamptz,
            '2026-11-10 12:00:00+03'::timestamptz,
            'Cancelled by the organizer after the county venue calendar changed. The page stays visible so registered visitors can see the cancellation notice.',
            'festival@coasthospitality.co.ke', 'https://picsum.photos/seed/ytci-lamu-festival/1600/900'
        )
)
INSERT INTO events (
    id, title, organizer, county, venue, event_date, end_date, start_time,
    end_time, type, status, description, contact_email, contact_phone,
    image_url, created_by, location_lat, location_lng, event_class, format,
    access, venue_id, organizer_profile_id, capacity, waitlist_capacity,
    registration_mode, price_amount, price_currency, sales_status, sales_end,
    ticket_url, timezone, version, published_at, cancelled_at, county_gid,
    author_ref, editorial_theme
)
SELECT
    s.id, s.title, o.display_name, s.county, v.name, s.event_date, s.end_date,
    s.start_time, s.end_time, s.theme, s.status, s.description, s.contact_email,
    o.contact_phone, s.image_url,
    (SELECT id FROM users WHERE email = 'grace@example.com'),
    v.latitude, v.longitude, s.event_class, s.format, s.access, v.id, o.id,
    s.capacity, s.waitlist_capacity, s.registration_mode, s.price_amount,
    CASE WHEN s.price_amount IS NOT NULL THEN 'KES'::char(3) ELSE NULL END,
    s.sales_status, s.sales_end, s.ticket_url, 'Africa/Nairobi', 1,
    s.published_at, s.cancelled_at, s.county_gid,
    (SELECT id FROM users WHERE email = 'grace@example.com'), s.theme
FROM seed s
JOIN venues v ON v.slug = s.venue_slug
JOIN organizer_profiles o ON o.slug = s.organizer_slug
ON CONFLICT (id) DO UPDATE SET
    title = EXCLUDED.title,
    organizer = EXCLUDED.organizer,
    county = EXCLUDED.county,
    venue = EXCLUDED.venue,
    event_date = EXCLUDED.event_date,
    end_date = EXCLUDED.end_date,
    start_time = EXCLUDED.start_time,
    end_time = EXCLUDED.end_time,
    type = EXCLUDED.type,
    status = EXCLUDED.status,
    description = EXCLUDED.description,
    contact_email = EXCLUDED.contact_email,
    contact_phone = EXCLUDED.contact_phone,
    image_url = EXCLUDED.image_url,
    location_lat = EXCLUDED.location_lat,
    location_lng = EXCLUDED.location_lng,
    event_class = EXCLUDED.event_class,
    format = EXCLUDED.format,
    access = EXCLUDED.access,
    venue_id = EXCLUDED.venue_id,
    organizer_profile_id = EXCLUDED.organizer_profile_id,
    capacity = EXCLUDED.capacity,
    waitlist_capacity = EXCLUDED.waitlist_capacity,
    registration_mode = EXCLUDED.registration_mode,
    price_amount = EXCLUDED.price_amount,
    price_currency = EXCLUDED.price_currency,
    sales_status = EXCLUDED.sales_status,
    sales_end = EXCLUDED.sales_end,
    ticket_url = EXCLUDED.ticket_url,
    timezone = EXCLUDED.timezone,
    version = EXCLUDED.version,
    published_at = EXCLUDED.published_at,
    cancelled_at = EXCLUDED.cancelled_at,
    county_gid = EXCLUDED.county_gid,
    author_ref = EXCLUDED.author_ref,
    editorial_theme = EXCLUDED.editorial_theme,
    updated_at = now();

INSERT INTO event_accessibility (
    event_id, step_free_entrance, step_free_route, accessible_toilet,
    accessible_parking, accessible_seating, captioning, sign_language,
    hearing_loop, quiet_room, assistance_dogs, companion_ticket_policy,
    content_notes, reported_by, verified_at
)
SELECT e.id, a.step_free_entrance, a.step_free_route, a.accessible_toilet,
       a.accessible_parking, a.accessible_seating, a.captioning, a.sign_language,
       a.hearing_loop, a.quiet_room, a.assistance_dogs, a.companion_ticket_policy,
       a.content_notes, a.reported_by, a.verified_at
FROM (
    VALUES
        (
            'b1000000-0000-4000-8000-000000000001'::uuid, 'yes', 'yes', 'yes', 'yes', 'yes', 'yes', 'no', 'yes', 'yes', 'yes',
            'One free companion ticket with every accessible-seat booking.',
            'Hybrid sessions are captioned; the quiet room is on level 2.',
            'Nairobi Convention Bureau', '2026-11-01 10:00:00+03'::timestamptz
        ),
        (
            'b1000000-0000-4000-8000-000000000002'::uuid, 'yes', 'yes', 'yes', 'yes', 'yes', 'yes', 'no', 'yes', 'yes', 'yes',
            'One free companion ticket with every accessible-seat booking.',
            'Live captioning in Lenana Hall. The quiet room is on level 2 beside the foyer.',
            'Nairobi Convention Bureau', '2026-11-05 10:00:00+03'::timestamptz
        ),
        (
            'b1000000-0000-4000-8000-000000000003'::uuid, 'yes', 'yes', 'yes', 'yes', 'yes', 'no', 'no', 'unknown', 'yes', 'yes',
            'Companion tickets are refundable on request; contact the events desk.',
            'Assistive listening is available in Bahari Ballroom on request at the registration desk.',
            'Coast Hospitality Association', '2026-11-18 10:00:00+03'::timestamptz
        ),
        (
            'b1000000-0000-4000-8000-000000000004'::uuid, 'yes', 'unknown', 'yes', 'no', 'yes', 'no', 'no', 'no', 'yes', 'yes',
            'Personal assistants attend free; state the requirement in the booking.',
            'Some garden paths are gravel. Adapted game-drive vehicles can be requested.',
            'Maasai Mara Tourism Alliance', '2026-11-10 10:00:00+03'::timestamptz
        ),
        (
            'b1000000-0000-4000-8000-000000000005'::uuid, 'yes', 'unknown', 'yes', 'no', 'yes', 'no', 'no', 'no', 'yes', 'yes',
            'Companion tickets are free for personal assistants.',
            'The main hall is step-free; the craft market is on a compacted gravel surface.',
            'Maasai Mara Tourism Alliance', '2026-08-20 10:00:00+03'::timestamptz
        ),
        (
            'b1000000-0000-4000-8000-000000000006'::uuid, 'yes', 'yes', 'yes', 'yes', 'yes', 'no', 'unknown', 'unknown', 'yes', 'yes',
            'Companion tickets are free for personal assistants.',
            'The workshop room and pool deck are step-free. The beach itself is not.',
            'Coast Hospitality Association', '2026-09-01 10:00:00+03'::timestamptz
        ),
        (
            'b1000000-0000-4000-8000-000000000007'::uuid, 'yes', 'yes', 'yes', 'yes', 'yes', 'yes', 'no', 'yes', 'yes', 'yes',
            'One free companion ticket with every accessible-seat booking.',
            'The craft hall has a quiet room and a low-sensory hour on the final morning.',
            'Nairobi Convention Bureau', '2026-11-12 10:00:00+03'::timestamptz
        ),
        (
            'b1000000-0000-4000-8000-000000000008'::uuid, 'yes', 'yes', 'yes', 'yes', 'yes', 'no', 'no', 'no', 'yes', 'yes',
            'Companion tickets are free for personal assistants.',
            'The event is cancelled. This accessibility record is retained with the past listing.',
            'Coast Hospitality Association', '2026-10-05 10:00:00+03'::timestamptz
        )
) AS a(event_id, step_free_entrance, step_free_route, accessible_toilet,
       accessible_parking, accessible_seating, captioning, sign_language,
       hearing_loop, quiet_room, assistance_dogs, companion_ticket_policy,
       content_notes, reported_by, verified_at)
JOIN events e ON e.id = a.event_id
ON CONFLICT (event_id) DO UPDATE SET
    step_free_entrance = EXCLUDED.step_free_entrance,
    step_free_route = EXCLUDED.step_free_route,
    accessible_toilet = EXCLUDED.accessible_toilet,
    accessible_parking = EXCLUDED.accessible_parking,
    accessible_seating = EXCLUDED.accessible_seating,
    captioning = EXCLUDED.captioning,
    sign_language = EXCLUDED.sign_language,
    hearing_loop = EXCLUDED.hearing_loop,
    quiet_room = EXCLUDED.quiet_room,
    assistance_dogs = EXCLUDED.assistance_dogs,
    companion_ticket_policy = EXCLUDED.companion_ticket_policy,
    content_notes = EXCLUDED.content_notes,
    reported_by = EXCLUDED.reported_by,
    verified_at = EXCLUDED.verified_at,
    updated_at = now();

-- ---------------------------------------------------------------------------
-- Agenda, speakers and speaker links
-- ---------------------------------------------------------------------------

INSERT INTO event_sessions (
    id, event_id, day, starts_at, ends_at, room, track, title, description,
    capacity, display_order
)
VALUES
    -- East Africa MICE Exchange 2027, day 1. Sessions 1 and 2 deliberately
    -- overlap in Lenana Hall so the agenda conflict UI has a real conflict.
    (
        'c1000000-0000-4000-8000-000000000001',
        'b1000000-0000-4000-8000-000000000002',
        '2027-02-18', '2027-02-18 09:00:00+03', '2027-02-18 10:15:00+03',
        'Lenana Hall', 'Plenary',
        'Opening plenary: the next decade of East African MICE',
        'Buyers and venue leaders set the agenda for the next decade: airlift, visa policy, venue supply and the delegate experience.',
        120, 1
    ),
    (
        'c1000000-0000-4000-8000-000000000002',
        'b1000000-0000-4000-8000-000000000002',
        '2027-02-18', '2027-02-18 09:30:00+03', '2027-02-18 10:45:00+03',
        'Lenana Hall', 'Market watch',
        'Regional outlook: demand, airlift and venue supply',
        'A data-led review of regional demand and what it means for Nairobi bids in 2027. This session overlaps the opening plenary.',
        80, 2
    ),
    (
        'c1000000-0000-4000-8000-000000000003',
        'b1000000-0000-4000-8000-000000000002',
        '2027-02-18', '2027-02-18 11:15:00+03', '2027-02-18 12:30:00+03',
        'Tsavo Ballroom', 'Associations',
        'Procurement clinics: winning international association bids',
        'Small-group clinics on bid documents, local host committees and the questions international boards actually ask.',
        40, 3
    ),
    (
        'c1000000-0000-4000-8000-000000000004',
        'b1000000-0000-4000-8000-000000000002',
        '2027-02-18', '2027-02-18 14:00:00+03', '2027-02-18 15:15:00+03',
        'Aberdare Room', 'Inclusion',
        'Accessibility from brief to badge',
        'How organizers can turn an accessibility claim into a verified, bookable experience, from site inspection to the badge desk.',
        50, 4
    ),
    (
        'c1000000-0000-4000-8000-000000000005',
        'b1000000-0000-4000-8000-000000000002',
        '2027-02-19', '2027-02-19 09:00:00+03', '2027-02-19 10:00:00+03',
        'Lenana Hall', 'Showcase',
        'Nairobi showcase and venue site inspections',
        'Short presentations from Nairobi venues followed by optional site inspections.',
        120, 5
    ),
    -- Mombasa Coastal Hospitality Forum 2026 (today, ongoing).
    (
        'c1000000-0000-4000-8000-000000000006',
        'b1000000-0000-4000-8000-000000000003',
        '2026-11-23', '2026-11-23 09:30:00+03', '2026-11-23 10:15:00+03',
        'Bahari Ballroom', 'Plenary',
        'Opening: coastal hospitality after the season',
        'A candid review of the 2026 coastal season and the staffing, pricing and access issues members want addressed before the next high season.',
        200, 1
    ),
    (
        'c1000000-0000-4000-8000-000000000007',
        'b1000000-0000-4000-8000-000000000003',
        '2026-11-23', '2026-11-23 11:00:00+03', '2026-11-23 12:00:00+03',
        'Coral Room', 'Operations',
        'Revenue management for independent hotels',
        'Practical rate and channel tactics for independent coastal properties, with a worked example for a 60-room hotel.',
        60, 2
    ),
    -- Narok Community Tourism Expo 2026 (past).
    (
        'c1000000-0000-4000-8000-000000000008',
        'b1000000-0000-4000-8000-000000000005',
        '2026-09-18', '2026-09-18 10:00:00+03', '2026-09-18 11:00:00+03',
        'Mara Conference Centre', 'Community',
        'Community conservancies and tourism revenue',
        'How conservancy fees and bed-night levies reach households, and what operators can do to make the split legible to visitors.',
        100, 1
    ),
    (
        'c1000000-0000-4000-8000-000000000009',
        'b1000000-0000-4000-8000-000000000005',
        '2026-09-18', '2026-09-18 11:30:00+03', '2026-09-18 12:30:00+03',
        'Oloololo Room', 'Experience',
        'Storytelling as a visitor experience',
        'A practical session for guides and hosts on building a cultural narrative without flattening the community it comes from.',
        60, 2
    )
ON CONFLICT (id) DO UPDATE SET
    event_id = EXCLUDED.event_id,
    day = EXCLUDED.day,
    starts_at = EXCLUDED.starts_at,
    ends_at = EXCLUDED.ends_at,
    room = EXCLUDED.room,
    track = EXCLUDED.track,
    title = EXCLUDED.title,
    description = EXCLUDED.description,
    capacity = EXCLUDED.capacity,
    display_order = EXCLUDED.display_order,
    updated_at = now();

INSERT INTO event_people (id, display_name, title, organization, bio, photo_url, is_public)
VALUES
    (
        'd1000000-0000-4000-8000-000000000001',
        'Amina Hassan',
        'Chief Executive, East Africa Tourism Board',
        'East Africa Tourism Board',
        'Amina leads regional destination marketing and has chaired three international association bids hosted in Nairobi and Mombasa.',
        'https://picsum.photos/seed/ytci-amina-hassan/320/320', true
    ),
    (
        'd1000000-0000-4000-8000-000000000002',
        'Peter Mwangi',
        'Director of Conventions, Nairobi Convention Bureau',
        'Nairobi Convention Bureau',
        'Peter works with association boards and venue teams on bid strategy, delegate services and post-event reporting.',
        'https://picsum.photos/seed/ytci-peter-mwangi/320/320', true
    ),
    (
        'd1000000-0000-4000-8000-000000000003',
        'Zawadi Otieno',
        'Head of Events, Sarova Hotels',
        'Sarova Hotels',
        'Zawadi runs conference and incentive operations across the coast and is a frequent trainer on service recovery at large events.',
        'https://picsum.photos/seed/ytci-zawadi-otieno/320/320', true
    ),
    (
        'd1000000-0000-4000-8000-000000000004',
        'Joseph Sironka',
        'Cultural guide and community ambassador',
        'Maasai Mara Tourism Alliance',
        'Joseph has guided in the Mara for eighteen years and works with conservancies on visitor programming that pays local storytellers directly.',
        'https://picsum.photos/seed/ytci-joseph-sironka/320/320', true
    ),
    (
        'd1000000-0000-4000-8000-000000000005',
        'Fatuma Ali',
        'Accessibility consultant',
        'Inclusive Kenya',
        'Fatuma audits venues and event operations against lived experience and supports organizers to publish accessibility claims they can stand behind.',
        'https://picsum.photos/seed/ytci-fatuma-ali/320/320', true
    ),
    (
        'd1000000-0000-4000-8000-000000000006',
        'Daniel Kimani',
        'MICE analyst',
        'East Africa Tourism Monitor',
        'Daniel tracks regional demand, airlift and venue supply and publishes the annual East Africa MICE outlook.',
        'https://picsum.photos/seed/ytci-daniel-kimani/320/320', true
    )
ON CONFLICT (id) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    title = EXCLUDED.title,
    organization = EXCLUDED.organization,
    bio = EXCLUDED.bio,
    photo_url = EXCLUDED.photo_url,
    is_public = EXCLUDED.is_public,
    updated_at = now();

INSERT INTO session_speakers (session_id, person_id, speaker_role, display_order)
VALUES
    ('c1000000-0000-4000-8000-000000000001', 'd1000000-0000-4000-8000-000000000001', 'speaker', 1),
    ('c1000000-0000-4000-8000-000000000001', 'd1000000-0000-4000-8000-000000000002', 'moderator', 2),
    ('c1000000-0000-4000-8000-000000000002', 'd1000000-0000-4000-8000-000000000006', 'moderator', 1),
    ('c1000000-0000-4000-8000-000000000002', 'd1000000-0000-4000-8000-000000000003', 'panelist', 2),
    ('c1000000-0000-4000-8000-000000000003', 'd1000000-0000-4000-8000-000000000002', 'speaker', 1),
    ('c1000000-0000-4000-8000-000000000003', 'd1000000-0000-4000-8000-000000000006', 'panelist', 2),
    ('c1000000-0000-4000-8000-000000000004', 'd1000000-0000-4000-8000-000000000005', 'speaker', 1),
    ('c1000000-0000-4000-8000-000000000004', 'd1000000-0000-4000-8000-000000000001', 'moderator', 2),
    ('c1000000-0000-4000-8000-000000000005', 'd1000000-0000-4000-8000-000000000004', 'host', 1),
    ('c1000000-0000-4000-8000-000000000006', 'd1000000-0000-4000-8000-000000000003', 'speaker', 1),
    ('c1000000-0000-4000-8000-000000000006', 'd1000000-0000-4000-8000-000000000001', 'moderator', 2),
    ('c1000000-0000-4000-8000-000000000007', 'd1000000-0000-4000-8000-000000000002', 'speaker', 1),
    ('c1000000-0000-4000-8000-000000000008', 'd1000000-0000-4000-8000-000000000004', 'speaker', 1),
    ('c1000000-0000-4000-8000-000000000009', 'd1000000-0000-4000-8000-000000000001', 'moderator', 1),
    ('c1000000-0000-4000-8000-000000000009', 'd1000000-0000-4000-8000-000000000003', 'panelist', 2)
ON CONFLICT (session_id, person_id) DO UPDATE SET
    speaker_role = EXCLUDED.speaker_role,
    display_order = EXCLUDED.display_order;

-- ---------------------------------------------------------------------------
-- Registrations
--
-- Includes a waitlisted attendee (Carol, MICE Exchange), a cancelled
-- registration, and access needs captured in answers. The Mombasa forum
-- attendees are organizer-imported paid delegates.
-- ---------------------------------------------------------------------------

INSERT INTO event_registrations (
    event_id, user_id, status, answers, reminder_offset_minutes,
    waitlist_position, cancel_reason, cancelled_at
)
SELECT s.event_id, u.id, s.status, s.answers, s.reminder, s.waitlist_position,
       s.cancel_reason, s.cancelled_at
FROM (
    VALUES
        (
            'b1000000-0000-4000-8000-000000000002'::uuid, 'alice@example.com', 'confirmed',
            '{"accessibility":"Wheelchair user. Step-free route and reserved seating required.","dietary":"Vegetarian","organization":"Inclusive Kenya"}'::jsonb,
            1440::int, NULL::int, NULL::text, NULL::timestamptz
        ),
        (
            'b1000000-0000-4000-8000-000000000002'::uuid, 'bob@example.com', 'confirmed',
            '{"organization":"Coast Hospitality Association","role":"Buyer"}'::jsonb,
            1440, NULL, NULL, NULL
        ),
        (
            'b1000000-0000-4000-8000-000000000002'::uuid, 'carol@example.com', 'waitlisted',
            '{"accessibility":"Requires a companion ticket and a seat near the aisle.","organization":"Narok County Government"}'::jsonb,
            1440, 1, NULL, NULL
        ),
        (
            'b1000000-0000-4000-8000-000000000002'::uuid, 'dave@example.com', 'cancelled',
            '{"organization":"East Africa Tourism Monitor"}'::jsonb,
            NULL, NULL, 'Travel dates changed', '2026-11-15 12:00:00+03'::timestamptz
        ),
        (
            'b1000000-0000-4000-8000-000000000003'::uuid, 'eve@example.com', 'confirmed',
            '{"ticketTier":"Delegate, full forum","accessibility":"Assistive listening required in the main hall.","purchasedVia":"Organizer handoff"}'::jsonb,
            1440, NULL, NULL, NULL
        ),
        (
            'b1000000-0000-4000-8000-000000000003'::uuid, 'alice@example.com', 'confirmed',
            '{"ticketTier":"Delegate, full forum","accessibility":"Step-free route required.","purchasedVia":"Organizer handoff"}'::jsonb,
            1440, NULL, NULL, NULL
        ),
        (
            'b1000000-0000-4000-8000-000000000003'::uuid, 'carol@example.com', 'confirmed',
            '{"ticketTier":"Delegate, forum plus site visit","purchasedVia":"Organizer handoff"}'::jsonb,
            1440, NULL, NULL, NULL
        ),
        (
            'b1000000-0000-4000-8000-000000000003'::uuid, 'bob@example.com', 'confirmed',
            '{"ticketTier":"Delegate, forum plus site visit","purchasedVia":"Organizer handoff"}'::jsonb,
            1440, NULL, NULL, NULL
        ),
        (
            'b1000000-0000-4000-8000-000000000005'::uuid, 'grace@example.com', 'confirmed',
            '{"community":"Narok","accessibility":"Step-free access required for a wheelchair user."}'::jsonb,
            1440, NULL, NULL, NULL
        ),
        (
            'b1000000-0000-4000-8000-000000000005'::uuid, 'joanadhiambo7@gmail.com', 'confirmed',
            '{"community":"Narok","role":"Homestay host"}'::jsonb,
            1440, NULL, NULL, NULL
        ),
        (
            'b1000000-0000-4000-8000-000000000005'::uuid, 'frank@example.com', 'confirmed',
            '{"community":"Narok","role":"Tour operator"}'::jsonb,
            1440, NULL, NULL, NULL
        ),
        (
            'b1000000-0000-4000-8000-000000000006'::uuid, 'alice@example.com', 'confirmed',
            '{"organization":"Inclusive Kenya","accessibility":"Step-free route required."}'::jsonb,
            1440, NULL, NULL, NULL
        ),
        (
            'b1000000-0000-4000-8000-000000000006'::uuid, 'dave@example.com', 'confirmed',
            '{"organization":"East Africa Tourism Monitor"}'::jsonb,
            1440, NULL, NULL, NULL
        ),
        (
            'b1000000-0000-4000-8000-000000000008'::uuid, 'alice@example.com', 'cancelled',
            '{"accessibility":"Step-free route required."}'::jsonb,
            NULL, NULL, 'Event cancelled by organizer', '2026-11-10 12:30:00+03'::timestamptz
        )
) AS s(event_id, email, status, answers, reminder, waitlist_position, cancel_reason, cancelled_at)
JOIN users u ON u.email = s.email
ON CONFLICT (event_id, user_id) DO UPDATE SET
    status = EXCLUDED.status,
    answers = EXCLUDED.answers,
    reminder_offset_minutes = EXCLUDED.reminder_offset_minutes,
    waitlist_position = EXCLUDED.waitlist_position,
    cancel_reason = EXCLUDED.cancel_reason,
    cancelled_at = EXCLUDED.cancelled_at,
    updated_at = now();

-- Paid events never create registrations through the app; these rows record
-- organizer ticket handoff interest only.
INSERT INTO event_ticket_handoffs (event_id, user_id, source)
SELECT s.event_id, u.id, s.source
FROM (
    VALUES
        ('b1000000-0000-4000-8000-000000000003'::uuid, 'joanadhiambo7@gmail.com', 'web'),
        ('b1000000-0000-4000-8000-000000000003'::uuid, 'frank@example.com', 'admin'),
        ('b1000000-0000-4000-8000-000000000003'::uuid, 'dave@example.com', 'mobile'),
        ('b1000000-0000-4000-8000-000000000004'::uuid, 'bob@example.com', 'mobile'),
        ('b1000000-0000-4000-8000-000000000004'::uuid, 'eve@example.com', 'web'),
        ('b1000000-0000-4000-8000-000000000007'::uuid, 'carol@example.com', 'web'),
        ('b1000000-0000-4000-8000-000000000007'::uuid, 'alice@example.com', 'mobile')
) AS s(event_id, email, source)
JOIN users u ON u.email = s.email
ON CONFLICT (event_id, user_id) DO UPDATE SET
    source = EXCLUDED.source;

-- ---------------------------------------------------------------------------
-- Tickets and check-ins
--
-- All check-ins are on one event (Mombasa Coastal Hospitality Forum 2026).
-- Eve's imported paid delegate has a QR ticket; Carol is left unchecked so the
-- admin check-in surface has a live registration to exercise.
-- ---------------------------------------------------------------------------

INSERT INTO event_tickets (registration_id, token_hash, issued_at, expires_at)
SELECT r.id,
       md5('ytci-mice-seed-ticket:' || r.id::text) || md5('ytci-mice-seed-ticket-v1:' || r.id::text),
       '2026-11-20 09:00:00+03'::timestamptz,
       '2026-11-24 17:00:00+03'::timestamptz
FROM event_registrations r
JOIN users u ON u.id = r.user_id
WHERE r.event_id = 'b1000000-0000-4000-8000-000000000003'
  AND u.email IN ('eve@example.com', 'alice@example.com')
ON CONFLICT (registration_id) DO UPDATE SET
    token_hash = EXCLUDED.token_hash,
    issued_at = EXCLUDED.issued_at,
    expires_at = EXCLUDED.expires_at,
    revoked_at = NULL,
    revoked_reason = NULL;

INSERT INTO event_checkins (
    registration_id, event_id, user_id, checked_in_at, method, staff_user_id,
    device_id, client_ts
)
SELECT r.id, r.event_id, r.user_id, s.checked_in_at, s.method,
       (SELECT id FROM users WHERE email = 'grace@example.com'),
       s.device_id, s.checked_in_at
FROM event_registrations r
JOIN users u ON u.id = r.user_id
JOIN (
    VALUES
        ('eve@example.com', '2026-11-23 09:15:00+03'::timestamptz, 'qr', 'seed-scanner-01'),
        ('alice@example.com', '2026-11-23 10:05:00+03'::timestamptz, 'manual', 'seed-desk-tablet')
) AS s(email, checked_in_at, method, device_id) ON s.email = u.email
WHERE r.event_id = 'b1000000-0000-4000-8000-000000000003'
ON CONFLICT (registration_id) DO NOTHING;

-- Keep registration state consistent with the check-in ledger on every run.
UPDATE event_registrations r
SET status = 'checked_in',
    checked_in_at = c.checked_in_at,
    updated_at = now()
FROM event_checkins c
WHERE c.registration_id = r.id
  AND c.event_id = 'b1000000-0000-4000-8000-000000000003';

COMMIT;
