-- ETL transform: legacy (production shape) -> public (new single-tenant schema).
-- Run inside the same database that has both schemas. Per docs/schema-migration-map.md.
-- Order respects FK dependencies.

-- ---------------------------------------------------------------------------
-- categories (categorias): tipo GASTO/RECEITA/BEM -> EXPENSE/INCOME/ASSET
-- ---------------------------------------------------------------------------
INSERT INTO public.categories (id, name, parent_category_id, type, created_at)
SELECT id,
       nome,
       categoria_pai_id,
       CASE tipo WHEN 'GASTO' THEN 'EXPENSE' WHEN 'RECEITA' THEN 'INCOME' WHEN 'BEM' THEN 'ASSET' END,
       created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.categorias;

-- ---------------------------------------------------------------------------
-- people (pessoas): drop tenant_id; derive gender from parentesco.
--   feminine pt -> 'F', masculine pt -> 'M', EU_MESMO -> 'M' (user is male),
--   OUTRO -> NULL. Also infer from existing edges (MAE_DE target's parent = F).
--   nickname/birth_date/notes/active/email (migration 000004) carried straight across — these
--   columns exist 1:1 in legacy.pessoas (apelido/data_nascimento/observacoes/ativo/email), no
--   translation needed beyond the rename.
-- ---------------------------------------------------------------------------
-- self_kinship: straight English translation of the old Parentesco enum, carried across verbatim
-- (never re-derived) — see migration 000005's doc comment for why this coexists with the
-- person_relationships graph rather than being backfilled into it.
INSERT INTO public.people (id, name, nickname, birth_date, gender, notes, active, email, self_kinship, created_at)
SELECT p.id,
       p.nome,
       p.apelido,
       p.data_nascimento,
       CASE
         WHEN p.parentesco IN ('MAE','FILHA','IRMA','SOBRINHA','SOGRA','NETA','AVO') THEN 'F'
         WHEN p.parentesco IN ('PAI','FILHO','IRMAO','SOBRINHO','SOGRO','NETO','AVO') THEN 'M'
         WHEN p.parentesco = 'EU_MESMO' THEN 'M'
         ELSE NULL
       END,
       p.observacoes,
       p.ativo,
       p.email,
       CASE p.parentesco
         WHEN 'EU_MESMO' THEN 'SELF' WHEN 'PAI' THEN 'FATHER' WHEN 'MAE' THEN 'MOTHER'
         WHEN 'IRMAO' THEN 'BROTHER' WHEN 'IRMA' THEN 'SISTER' WHEN 'FILHO' THEN 'SON'
         WHEN 'FILHA' THEN 'DAUGHTER' WHEN 'AVO' THEN 'GRANDPARENT' WHEN 'NETO' THEN 'GRANDSON'
         WHEN 'NETA' THEN 'GRANDDAUGHTER' WHEN 'TIO' THEN 'UNCLE' WHEN 'TIA' THEN 'AUNT'
         WHEN 'SOBRINHO' THEN 'NEPHEW' WHEN 'SOBRINHA' THEN 'NIECE' WHEN 'SOGRO' THEN 'FATHER_IN_LAW'
         WHEN 'SOGRA' THEN 'MOTHER_IN_LAW' WHEN 'GENRO' THEN 'SON_IN_LAW' WHEN 'NORA' THEN 'DAUGHTER_IN_LAW'
         WHEN 'CONJUGE' THEN 'SPOUSE' ELSE 'OTHER'
       END,
       p.created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.pessoas p;

-- Fix gender from parent edges (a mother/father edge implies the subject's gender).
UPDATE public.people
SET gender = 'F'
WHERE id IN (SELECT de_id FROM legacy.pessoa_relacoes WHERE tipo = 'MAE_DE')
  AND gender IS NULL;
UPDATE public.people
SET gender = 'M'
WHERE id IN (SELECT de_id FROM legacy.pessoa_relacoes WHERE tipo = 'PAI_DE')
  AND gender IS NULL;

-- ---------------------------------------------------------------------------
-- person_relationships (pessoa_relacoes): de_id/para_id -> from/to, MAE_DE etc -> MOTHER_OF
-- ---------------------------------------------------------------------------
INSERT INTO public.person_relationships (id, from_person_id, to_person_id, type, created_at)
SELECT id,
       CASE WHEN r.de_id IN (SELECT id FROM legacy.pessoas) THEN r.de_id END,
       CASE WHEN r.para_id IN (SELECT id FROM legacy.pessoas) THEN r.para_id END,
       CASE tipo WHEN 'MAE_DE' THEN 'MOTHER_OF' WHEN 'PAI_DE' THEN 'FATHER_OF' WHEN 'CONJUGE_DE' THEN 'SPOUSE_OF' END,
       created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.pessoa_relacoes r;

-- ---------------------------------------------------------------------------
-- pets (especie -> species) / pet_records — inserted BEFORE assets/documents because those
-- reference pets(id).
-- ---------------------------------------------------------------------------
INSERT INTO public.pets (id, name, species, breed, birth_date, notes, active, created_at)
SELECT id, nome,
       CASE COALESCE(NULLIF(especie, ''), 'OUTRO') WHEN 'CACHORRO' THEN 'DOG' WHEN 'GATO' THEN 'CAT' WHEN 'PASSARO' THEN 'BIRD'
                    WHEN 'PEIXE' THEN 'FISH' ELSE 'OTHER' END,
       raca, data_nascimento, observacoes, ativo,
       created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.pets;

INSERT INTO public.pet_records (id, pet_id, type, name, record_date, status, amount, notes, created_at)
SELECT id, pet_id, tipo, nome, data,
       CASE COALESCE(NULLIF(status, ''), 'REALIZADO')
            WHEN 'REALIZADO' THEN 'DONE' WHEN 'PENDENTE' THEN 'PENDING' WHEN 'CANCELADO' THEN 'CANCELLED' END,
       valor, observacoes, created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.registros_pet;

-- ---------------------------------------------------------------------------
-- assets (bens): pessoa_id, categoria_id kept; atributos jsonb kept; status kept (ACTIVE default).
-- ---------------------------------------------------------------------------
INSERT INTO public.assets (id, person_id, name, category_id, identification, acquisition_value,
       acquisition_date, current_estimated_value, status, has_debt, debt_amount, notes, attributes, created_at)
SELECT id,
       CASE WHEN b.pessoa_id IN (SELECT id FROM legacy.pessoas) THEN b.pessoa_id END,
       nome,
       CASE WHEN b.categoria_id IN (SELECT id FROM legacy.categorias) THEN b.categoria_id END,
       identificacao, valor_aquisicao, data_aquisicao,
       valor_atual_estimado,
       CASE COALESCE(NULLIF(status, ''), 'ATIVO') WHEN 'ATIVO' THEN 'ACTIVE' WHEN 'INATIVO' THEN 'INACTIVE'
            WHEN 'VENDIDO' THEN 'SOLD' ELSE 'ACTIVE' END,
       tem_divida, valor_divida, observacoes,
       atributos, created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.bens b;

-- ---------------------------------------------------------------------------
-- documents (documentos): pessoa_id, asset_id, pet_id kept; file_type from origem? keep 'OTHER'
--   if unknown — we only have origem, default PDF. Set file_type from origem mapping if present,
--   else 'OTHER'.
-- ---------------------------------------------------------------------------
INSERT INTO public.documents (id, person_id, original_filename, file_path, file_type, origin,
       document_date, tags, asset_id, pet_id, created_at)
SELECT id,
       CASE WHEN d.pessoa_id IN (SELECT id FROM legacy.pessoas) THEN d.pessoa_id END,
       COALESCE(nome_arquivo_original, 'arquivo'), arquivo_path,
       CASE WHEN arquivo_path ILIKE '%.pdf' THEN 'PDF'
            WHEN arquivo_path ILIKE '%.png' THEN 'PNG'
            WHEN arquivo_path ILIKE '%.jpg' OR arquivo_path ILIKE '%.jpeg' THEN 'JPEG'
            WHEN arquivo_path ILIKE '%.webp' THEN 'WEBP'
            ELSE 'OTHER' END,
       COALESCE(origem, 'MANUAL'),
       data_documento, tags,
       CASE WHEN d.bem_id IN (SELECT id FROM legacy.bens) THEN d.bem_id END,
       CASE WHEN d.pet_id IN (SELECT id FROM legacy.pets) THEN d.pet_id END,
       created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.documentos d;

-- ---------------------------------------------------------------------------
-- clients (clientes)
-- ---------------------------------------------------------------------------
INSERT INTO public.clients (id, name, phone, email, cpf_cnpj, notes, active, pix_key, created_at)
SELECT id, nome, telefone, email, cpf_cnpj, observacoes, ativo, pix_key,
       created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.clientes;

-- ---------------------------------------------------------------------------
-- loans (emprestimos): taxa_juros_mensal numeric(5,2) -> monthly_interest_rate numeric(12,6);
--   tipo_pagamento kept as varchar(20) (FIXED default); status translated.
-- ---------------------------------------------------------------------------
INSERT INTO public.loans (id, client_id, principal_amount, monthly_interest_rate, payment_type,
       installment_count, contract_date, status, outstanding_balance, due_day, paid_off_date,
       legacy_source, created_at)
SELECT id, cliente_id, valor_principal, taxa_juros_mensal::numeric(12,6),
       CASE COALESCE(NULLIF(tipo_pagamento, ''), 'PARCELADO')
            WHEN 'PARCELADO' THEN 'INSTALLMENT' WHEN 'UNICO' THEN 'RECURRING_INTEREST'
            ELSE 'INSTALLMENT' END,
       quantidade_parcelas, data_contrato,
       CASE COALESCE(NULLIF(status, ''), 'ATIVO')
            WHEN 'ATIVO' THEN 'ACTIVE' WHEN 'INADIMPLENTE' THEN 'OVERDUE'
            WHEN 'QUITADO' THEN 'PAID_OFF' WHEN 'CANCELADO' THEN 'CANCELLED' ELSE 'ACTIVE' END,
       saldo_devedor, dia_vencimento, data_quitacao, origem_legado,
       created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.emprestimos e
WHERE e.cliente_id IN (SELECT id FROM legacy.clientes);

-- ---------------------------------------------------------------------------
-- installments (parcelas): status translated.
-- ---------------------------------------------------------------------------
INSERT INTO public.installments (id, loan_id, installment_number, due_date, installment_amount,
       outstanding_balance, principal_amount, interest_amount, status, created_at)
SELECT id, emprestimo_id, numero_parcela, data_vencimento, valor_parcela, saldo_devedor_parcela,
       valor_principal, valor_juros,
       CASE COALESCE(NULLIF(status, ''), 'PENDENTE')
            WHEN 'PENDENTE' THEN 'PENDING' WHEN 'PAGA' THEN 'PAID' WHEN 'PAGO' THEN 'PAID'
            WHEN 'ATRASADO' THEN 'OVERDUE' WHEN 'CANCELADO' THEN 'CANCELLED' END,
       created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.parcelas p
WHERE p.emprestimo_id IN (SELECT id FROM legacy.emprestimos);

-- ---------------------------------------------------------------------------
-- loan_transactions (movimentacoes): tipo translated.
-- ---------------------------------------------------------------------------
INSERT INTO public.loan_transactions (id, client_id, loan_id, installment_id, type, amount,
       payment_date, description, legacy_source, created_at)
SELECT id, cliente_id,
       CASE WHEN m.emprestimo_id IN (SELECT id FROM legacy.emprestimos) THEN m.emprestimo_id END,
       CASE WHEN m.parcela_id IN (SELECT id FROM legacy.parcelas) THEN m.parcela_id END,
       CASE tipo WHEN 'DESEMBOLSO' THEN 'DISBURSEMENT' WHEN 'RECEBIMENTO_PARCELA' THEN 'INSTALLMENT_PAYMENT'
                 WHEN 'RECEBIMENTO_AVULSO' THEN 'ONE_OFF_PAYMENT' WHEN 'PERDAO' THEN 'FORGIVENESS'
                 WHEN 'AMORTIZACAO' THEN 'AMORTIZATION' END,
       valor, data_pagamento, descricao, origem_legado,
       created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.movimentacoes m
WHERE m.cliente_id IN (SELECT id FROM legacy.clientes);

-- ---------------------------------------------------------------------------
-- expenses (gastos) / income_entries (receitas)
-- ---------------------------------------------------------------------------
INSERT INTO public.expenses (id, document_id, category_id, person_id, amount, expense_date,
       description, reviewed, original_date, recurring, created_at)
SELECT id,
       CASE WHEN e.documento_id IN (SELECT id FROM legacy.documentos) THEN e.documento_id END,
       CASE WHEN e.categoria_id IN (SELECT id FROM legacy.categorias) THEN e.categoria_id END,
       CASE WHEN e.pessoa_id IN (SELECT id FROM legacy.pessoas) THEN e.pessoa_id END,
       valor, data, descricao, revisado,
       data_original, recorrente, created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.gastos e;

INSERT INTO public.income_entries (id, document_id, category_id, person_id, amount, income_date,
       original_date, description, reviewed, created_at)
SELECT id,
       CASE WHEN r.documento_id IN (SELECT id FROM legacy.documentos) THEN r.documento_id END,
       CASE WHEN r.categoria_id IN (SELECT id FROM legacy.categorias) THEN r.categoria_id END,
       CASE WHEN r.pessoa_id IN (SELECT id FROM legacy.pessoas) THEN r.pessoa_id END,
       valor, data, data_original, descricao,
       revisado, created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.receitas r;

-- ---------------------------------------------------------------------------
-- health_marker_catalog / health_records (registros_saude)
-- ---------------------------------------------------------------------------
INSERT INTO public.health_marker_catalog (id, name, unit, reference_min, reference_max,
       related_organ, created_at)
SELECT id, nome, unidade, valor_referencia_min, valor_referencia_max, orgao_relacionado,
       created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.marcadores_exame_catalogo;

INSERT INTO public.health_records (id, person_id, type, record_date, status, amount, unit,
       marker_catalog_id, organ, assessment_status, document_id, notes, created_at)
SELECT id, pessoa_id,
       -- tipo (TipoRegistroSaude) -> health.RecordType (internal/health/domain.go). This CASE was
       -- missing: `tipo` was copied verbatim, so PESO/ALTURA/EXAME_LABORATORIAL/CONDICAO_MEDICA
       -- landed untranslated and every health screen read "Nao medido".
       CASE COALESCE(NULLIF(tipo, ''), 'NOTA')
            WHEN 'PESO' THEN 'WEIGHT'
            WHEN 'ALTURA' THEN 'HEIGHT'
            WHEN 'PRESSAO_ARTERIAL' THEN 'BLOOD_PRESSURE'
            WHEN 'PERCENTUAL_GORDURA' THEN 'BODY_FAT_PCT'
            WHEN 'MASSA_MUSCULAR' THEN 'MUSCLE_MASS'
            WHEN 'CIRCUNFERENCIA_CINTURA' THEN 'WAIST_CIRC'
            WHEN 'CIRCUNFERENCIA_QUADRIL' THEN 'HIP_CIRC'
            WHEN 'GORDURA_VISCERAL' THEN 'VISCERAL_FAT'
            WHEN 'TAXA_METABOLICA_BASAL' THEN 'BMR'
            WHEN 'EXAME_LABORATORIAL' THEN 'LAB_RESULT'
            WHEN 'AVALIACAO_ORGAO' THEN 'ORGAN_ASSESSMENT'
            WHEN 'VACINA' THEN 'VACCINE'
            WHEN 'CHECKUP' THEN 'CHECKUP'
            WHEN 'CONDICAO_MEDICA' THEN 'MEDICAL_CONDITION'
            WHEN 'MEDICAMENTO' THEN 'MEDICATION'
            WHEN 'TIPO_SANGUINEO' THEN 'BLOOD_TYPE'
            WHEN 'ALERGIA' THEN 'ALLERGY'
            WHEN 'HABITO' THEN 'HABIT'
            WHEN 'HISTORICO_FAMILIAR' THEN 'FAMILY_HISTORY'
            WHEN 'NOTA' THEN 'NOTE'
            ELSE 'NOTE' END,
       data,
       CASE COALESCE(NULLIF(status, ''), 'REALIZADO')
            WHEN 'REALIZADO' THEN 'DONE' WHEN 'PENDENTE' THEN 'PENDING' WHEN 'CANCELADO' THEN 'CANCELLED' END,
       valor, unidade,
       CASE WHEN h.marcador_catalogo_id IN (SELECT id FROM legacy.marcadores_exame_catalogo) THEN h.marcador_catalogo_id END,
       orgao, avaliacao_status,
       CASE WHEN h.documento_id IN (SELECT id FROM legacy.documentos) THEN h.documento_id END,
       observacoes,
       created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.registros_saude h
WHERE h.pessoa_id IN (SELECT id FROM legacy.pessoas);

-- ---------------------------------------------------------------------------
-- financial_balances / financial_balance_history (saldos_financeiros / _historico)
-- ---------------------------------------------------------------------------
INSERT INTO public.financial_balances (id, person_id, name, balance_type, current_value,
       updated_on, notes, created_at)
SELECT id, pessoa_id, nome,
       CASE COALESCE(NULLIF(tipo, ''), 'OUTRO')
            WHEN 'FGTS' THEN 'FGTS' WHEN 'PREVIDENCIA' THEN 'PRIVATE_PENSION'
            WHEN 'PREVIDENCIA_PRIVADA' THEN 'PRIVATE_PENSION'
            WHEN 'POUPANCA' THEN 'SAVINGS' WHEN 'CONTA_CORRENTE' THEN 'CHECKING' ELSE 'OTHER' END,
       valor_atual, atualizado_em, observacoes,
       created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.saldos_financeiros;

INSERT INTO public.financial_balance_history (id, balance_id, balance, recorded_on, created_at)
SELECT id, saldo_financeiro_id, valor_atual, atualizado_em,
       created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.saldos_financeiros_historico h
WHERE h.saldo_financeiro_id IN (SELECT id FROM legacy.saldos_financeiros);

-- ---------------------------------------------------------------------------
-- app_users / member_permissions (no rows seeded — single-tenant, no members yet)
-- ---------------------------------------------------------------------------
INSERT INTO public.app_users (id, email, role, password_hash, name, last_login_at, created_at)
SELECT id, email,
       CASE COALESCE(NULLIF(role, ''), 'TENANT_MEMBER')
            WHEN 'SUPER_ADMIN' THEN 'OWNER' WHEN 'TENANT_OWNER' THEN 'OWNER'
            WHEN 'TENANT_MEMBER' THEN 'MEMBER' ELSE 'MEMBER' END,
       senha_hash, nome, last_login_at AT TIME ZONE 'America/Sao_Paulo',
       created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.app_users;

-- member_permissions: D-012 (AGREED) — seed one grant for the MEMBER demo user so the
-- "MEMBER allowed" path of RequirePerm is testable end-to-end (OWNER uses bypass).
INSERT INTO public.member_permissions (user_id, module, can_read, can_write, created_at)
SELECT id, 'expenses', true, true, now()
FROM public.app_users WHERE email = 'sanches57@uni9.edu.br' AND role = 'MEMBER';

-- ---------------------------------------------------------------------------
-- person_information (pessoa_informacoes) — drop tenant_id.
-- ---------------------------------------------------------------------------
INSERT INTO public.person_information (id, person_id, category, key, value, document_id, origin,
       document_type, created_at)
SELECT id, pessoa_id,
       CASE COALESCE(NULLIF(categoria, ''), 'OUTRO')
            WHEN 'PESSOAL' THEN 'PERSONAL' WHEN 'ACADEMICA' THEN 'ACADEMIC' WHEN 'ACADEMICO' THEN 'ACADEMIC'
            WHEN 'PROFISSIONAL' THEN 'PROFESSIONAL' WHEN 'MEDICO' THEN 'MEDICAL' ELSE 'OTHER' END,
       chave, valor,
       CASE WHEN pi.documento_id IN (SELECT id FROM legacy.documentos) THEN pi.documento_id END,
       CASE COALESCE(NULLIF(origem, ''), 'MANUAL')
            WHEN 'EXTRAIDO' THEN 'IMPORT' WHEN 'MANUAL' THEN 'MANUAL'
            WHEN 'IMPORT' THEN 'IMPORT' WHEN 'OCR' THEN 'OCR' ELSE 'MANUAL' END,
       tipo_documento,
       created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.pessoa_informacoes pi
WHERE pi.pessoa_id IN (SELECT id FROM legacy.pessoas);

-- ---------------------------------------------------------------------------
-- Reference tables carried as-is (no tenant_id, no translation needed).
-- ---------------------------------------------------------------------------
INSERT INTO public.notification_configuration (id, retention_days, created_at)
SELECT id, retencao_dias, created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.notificacao_configuracao;

INSERT INTO public.backup_configuration (id, hour, minute, last_automatic_backup_at, created_at)
SELECT id, hora, minuto, ultimo_backup_automatico_em AT TIME ZONE 'America/Sao_Paulo',
       created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.backup_configuracao;

INSERT INTO public.ai_configurations (id, provider_order, created_at)
SELECT id,
       CASE WHEN ordem_provedores IS NULL OR ordem_provedores = '' THEN '[]'::jsonb
            ELSE to_jsonb(string_to_array(ordem_provedores, ',')) END,
       created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.ia_configuracoes;

-- ---------------------------------------------------------------------------
-- chat_messages (papel -> role), external_integrations (tipo -> canonical), notifications.
-- ---------------------------------------------------------------------------
INSERT INTO public.chat_messages (id, app_user_id, role, content, model_used, tool_calls, created_at)
SELECT id, app_user_id,
       CASE COALESCE(NULLIF(papel, ''), 'USER')
            WHEN 'USUARIO' THEN 'USER' WHEN 'ASSISTENTE' THEN 'ASSISTANT' WHEN 'SISTEMA' THEN 'SYSTEM'
            ELSE 'USER' END,
       conteudo, modelo_usado,
       CASE WHEN ferramentas_chamadas IS NULL OR ferramentas_chamadas = '' THEN NULL
            ELSE ferramentas_chamadas::jsonb END,
       created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.chat_mensagens;

INSERT INTO public.external_integrations (id, type, connected_account, refresh_token_encrypted,
       refresh_token_iv, models, last_telegram_update_id, telegram_pairing_code,
       telegram_pairing_expires_at, immich_base_url, created_at)
SELECT id,
       -- Stale mapping fixed: migration 000002 (D-013) already widened the CHECK to the real 7-value
       -- Java enum (GOOGLE_DRIVE/GEMINI/DEEPSEEK/GROQ/OPENROUTER/TELEGRAM/IMMICH) + OPENAI/OTHER as
       -- additive extras — this CASE used to collapse 4 of those 7 real values down to OTHER/OPENAI
       -- as if the narrower pre-000002 CHECK still applied, silently losing e.g. a real GOOGLE_DRIVE
       -- connection's type on every ETL run.
       CASE COALESCE(NULLIF(tipo, ''), 'OTHER')
            WHEN 'IMMICH' THEN 'IMMICH' WHEN 'TELEGRAM' THEN 'TELEGRAM' WHEN 'OPENAI' THEN 'OPENAI'
            WHEN 'OPENROUTER' THEN 'OPENROUTER' WHEN 'DEEPSEEK' THEN 'DEEPSEEK' WHEN 'GEMINI' THEN 'GEMINI'
            WHEN 'GROQ' THEN 'GROQ' WHEN 'GOOGLE_DRIVE' THEN 'GOOGLE_DRIVE' ELSE 'OTHER' END,
       conta_conectada, refresh_token_criptografado, refresh_token_iv,
       CASE WHEN modelos IS NULL OR modelos = '' THEN NULL ELSE to_jsonb(string_to_array(modelos, ',')) END,
       ultimo_update_id_telegram, telegram_codigo_pareamento,
       telegram_pareamento_expira_em AT TIME ZONE 'America/Sao_Paulo',
       immich_base_url,
       created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.integracoes_externas;

INSERT INTO public.notifications (id, type, title, message, link, read, created_at)
SELECT id, tipo, titulo, mensagem, link, lida,
       created_at AT TIME ZONE 'America/Sao_Paulo'
FROM legacy.notificacoes;
