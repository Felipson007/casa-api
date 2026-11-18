package documents

import "strings"

var originSummaryLabels = map[Origin]string{
	OriginContract: "contrato ou acordo",
	OriginInvoice:  "fatura financeira", OriginPaystub: "holerite ou comprovante de renda",
	OriginLabResult: "exame ou laudo de saúde", OriginPersonal: "documento pessoal",
	OriginAsset: "documento de patrimônio", OriginPet: "documento de pet",
	OriginInsurance: "documento de seguro", OriginWarranty: "documento de garantia",
	OriginTax: "documento fiscal", OriginOther: "documento ainda não classificado",
}

func readableDocumentName(filename string) string {
	name := strings.NewReplacer("_", " ", "-", " ").Replace(filename)
	return strings.Join(strings.Fields(name), " ")
}

func generatedDocumentDescription(d Document) string {
	label := originSummaryLabels[d.Origin]
	if label == "" {
		label = "documento"
	}
	return "Documento do tipo " + label + ": " + readableDocumentName(d.OriginalFilename) + "."
}

func ensureDocumentSearchMetadata(d *Document) {
	if d.Description == nil || strings.TrimSpace(*d.Description) == "" || strings.HasPrefix(*d.Description, "Documento do tipo ") {
		description := generatedDocumentDescription(*d)
		d.Description = &description
	}
	classificationTag := "origem:" + strings.ToLower(string(d.Origin))
	fileTypeTag := "arquivo:" + strings.ToLower(string(d.FileType))
	if d.Tags == nil || strings.TrimSpace(*d.Tags) == "" {
		tags := classificationTag + "," + fileTypeTag
		d.Tags = &tags
		return
	}
	tags := *d.Tags
	if !strings.Contains(tags, classificationTag) {
		tags += "," + classificationTag
	}
	if !strings.Contains(tags, fileTypeTag) {
		tags += "," + fileTypeTag
	}
	d.Tags = &tags
}
