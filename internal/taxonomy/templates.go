package taxonomy

import (
	"strings"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// opts joins choices the way a field stores them: one per line.
func opts(choices ...string) string { return strings.Join(choices, "\n") }

// Templates returns the starter trees an operator can take in one click, in the
// order they are offered.
//
// ⚠ NOTHING HERE IS A FIELD THE OFFER ALREADY CARRIES. Title, description,
// price, reference, quantity, location and the photographs are columns on the
// offer itself, and a template that asked for them again would give an operator
// two boxes for one fact and two answers that disagree. That rule is why the
// car template has no "Kaina", no "SKU", no "Lokacija" and no "Aprašymas"
// despite all four appearing on the form it was read from.
func Templates() []core.CategoryTemplate {
	return []core.CategoryTemplate{carParts(), pcParts()}
}

// Template returns the template addressed by code, upper-cased, and whether
// there is one.
func Template(code string) (core.CategoryTemplate, bool) {
	code = strings.ToUpper(strings.TrimSpace(code))
	for _, t := range Templates() {
		if t.Code == code {
			return t, true
		}
	}
	return core.CategoryTemplate{}, false
}

// carParts is a used-car-parts yard, read from the five-step "Pridėti naują
// detalę" form at app.recar.lt that M sent in as the worked example.
//
// ★ IT IS ONE NODE ON PURPOSE, and that is a claim about the trade rather than
// about effort. Every question here describes either the DONOR VEHICLE or the
// part's own identity, and both are true of any part off that car — a wing
// mirror and a gearbox are described by the same make, model, year and VIN. So
// the questions belong at the root, where ADR-021's inheritance hands them to
// every subcategory the yard grows later without anyone re-entering them.
func carParts() core.CategoryTemplate {
	return core.CategoryTemplate{
		Code:    "CAR",
		Name:    "Car parts",
		Summary: "The donor vehicle, OEM codes and a condition grade — from the recar.lt intake form.",
		Fields: []core.CategoryField{
			// The donor vehicle. Make, model and year carry the red asterisk on
			// the source form, and they are also what a marketplace matches a
			// part against, which is why those three and the codes below are the
			// ones ticked for export.
			{Code: "make", Label: "Make", Kind: core.FieldText, Required: true, Export: true},
			{Code: "model", Label: "Model", Kind: core.FieldText, Required: true, Export: true},
			{Code: "modification", Label: "Modification", Kind: core.FieldText},
			{Code: "engine_code", Label: "Engine code", Kind: core.FieldText, Export: true},
			{Code: "year", Label: "Year", Kind: core.FieldNumber, Required: true, Export: true},
			{Code: "vin", Label: "VIN", Kind: core.FieldText},
			{Code: "body_type", Label: "Body type", Kind: core.FieldChoice, Options: opts(
				"Hatchback", "Saloon", "Estate", "Coupé", "Convertible",
				"SUV", "MPV", "Pickup", "Van")},
			{Code: "fuel_type", Label: "Fuel type", Kind: core.FieldChoice, Export: true, Options: opts(
				"Petrol", "Diesel", "Hybrid", "Plug-in hybrid", "Electric", "LPG", "CNG")},
			{Code: "displacement", Label: "Engine displacement", Kind: core.FieldNumber, Unit: "cm³"},
			{Code: "gearbox", Label: "Gearbox", Kind: core.FieldChoice, Options: opts(
				"Manual", "Automatic", "Semi-automatic", "CVT")},
			{Code: "drivetrain", Label: "Drivetrain", Kind: core.FieldChoice, Options: opts(
				"Front-wheel drive", "Rear-wheel drive", "All-wheel drive")},
			{Code: "mileage", Label: "Mileage", Kind: core.FieldNumber, Unit: "km"},
			{Code: "steering", Label: "Steering side", Kind: core.FieldChoice, Options: opts(
				"Left-hand drive", "Right-hand drive")},
			{Code: "colour", Label: "Colour", Kind: core.FieldChoice, Options: opts(
				"Black", "White", "Silver", "Grey", "Blue", "Red", "Green",
				"Yellow", "Brown", "Beige", "Orange", "Gold", "Purple")},
			{Code: "colour_code", Label: "Colour code", Kind: core.FieldText},

			// The part itself.
			{Code: "oem_main", Label: "Main OEM code", Kind: core.FieldText, Export: true},
			{Code: "oem_other", Label: "Additional OEM codes", Kind: core.FieldText},
			{Code: "defect", Label: "Defect description", Kind: core.FieldLongText},
			// The source form grades a part A/B/C and then offers a canned
			// sentence about its condition. The GRADE is here because it is
			// structured, sortable and worth publishing; the sentence is not,
			// because an offer already carries a Condition of its own and a
			// second free-text description of the same thing is how two answers
			// come to disagree.
			{Code: "grade", Label: "Condition grade", Kind: core.FieldChoice, Export: true, Options: opts(
				"A — as new, no visible defects",
				"B — used, good condition",
				"C — used, visible wear")},
		},
	}
}

// pcParts is the second trade M asked for, and it is shaped the opposite way to
// the first.
//
// ★ IT HAS CHILDREN BECAUSE ITS QUESTIONS ARE NOT UNIVERSAL. A capacity in
// gigabytes is a fact about a drive and meaningless about a case, so asking it
// of every PC part would train an operator to skip questions — which is the
// habit that empties a taxonomy. Only what identifies ANY component sits at the
// root; each subcategory adds what is true of it alone.
func pcParts() core.CategoryTemplate {
	grade := core.CategoryField{
		Code: "grade", Label: "Condition grade", Kind: core.FieldChoice, Export: true,
		Options: opts(
			"A — as new, no visible defects",
			"B — used, good condition",
			"C — used, visible wear"),
	}
	return core.CategoryTemplate{
		Code:    "PC",
		Name:    "PC parts",
		Summary: "What identifies any component, plus six subcategories with the specs that are true of each.",
		Fields: []core.CategoryField{
			{Code: "brand", Label: "Brand", Kind: core.FieldText, Required: true, Export: true},
			{Code: "model", Label: "Model", Kind: core.FieldText, Required: true, Export: true},
			{Code: "mpn", Label: "Manufacturer part number", Kind: core.FieldText, Export: true},
			// A tested component and an untested one are different products at
			// different prices, and the difference is not visible in a
			// photograph — so it is a question rather than something to write
			// into a description and hope.
			{Code: "tested", Label: "Tested working", Kind: core.FieldBool, Export: true},
			grade,
		},
		Children: []core.TemplateChild{
			{Code: "GPU", Name: "Graphics cards", Fields: []core.CategoryField{
				{Code: "vram", Label: "VRAM", Kind: core.FieldNumber, Unit: "GB", Export: true},
				{Code: "interface", Label: "Interface", Kind: core.FieldChoice, Options: opts(
					"PCIe 5.0 x16", "PCIe 4.0 x16", "PCIe 3.0 x16", "PCIe 2.0 x16", "AGP", "PCI")},
				{Code: "length", Label: "Card length", Kind: core.FieldNumber, Unit: "mm"},
			}},
			{Code: "RAM", Name: "Memory", Fields: []core.CategoryField{
				{Code: "capacity", Label: "Capacity", Kind: core.FieldNumber, Unit: "GB", Export: true},
				{Code: "speed", Label: "Speed", Kind: core.FieldNumber, Unit: "MHz", Export: true},
				{Code: "memory_type", Label: "Type", Kind: core.FieldChoice, Export: true, Options: opts(
					"DDR5", "DDR4", "DDR3", "DDR2")},
			}},
			{Code: "SSD", Name: "Storage", Fields: []core.CategoryField{
				{Code: "capacity", Label: "Capacity", Kind: core.FieldNumber, Unit: "GB", Export: true},
				{Code: "form_factor", Label: "Form factor", Kind: core.FieldChoice, Options: opts(
					"M.2 2280", "M.2 2242", "2.5\"", "3.5\"", "mSATA")},
				{Code: "interface", Label: "Interface", Kind: core.FieldChoice, Export: true, Options: opts(
					"NVMe PCIe 5.0", "NVMe PCIe 4.0", "NVMe PCIe 3.0", "SATA III", "SAS", "IDE / PATA")},
			}},
			{Code: "CPU", Name: "Processors", Fields: []core.CategoryField{
				// Socket is text rather than a choice: the list gains entries
				// every hardware generation, and a closed list that is one
				// release out of date stops an operator filing a part at all.
				{Code: "socket", Label: "Socket", Kind: core.FieldText, Export: true},
				{Code: "cores", Label: "Cores", Kind: core.FieldNumber, Export: true},
				{Code: "base_clock", Label: "Base clock", Kind: core.FieldNumber, Unit: "GHz"},
			}},
			{Code: "MB", Name: "Motherboards", Fields: []core.CategoryField{
				{Code: "socket", Label: "Socket", Kind: core.FieldText, Export: true},
				{Code: "chipset", Label: "Chipset", Kind: core.FieldText, Export: true},
				{Code: "form_factor", Label: "Form factor", Kind: core.FieldChoice, Options: opts(
					"ATX", "Micro-ATX", "Mini-ITX", "E-ATX")},
			}},
			{Code: "PSU", Name: "Power supplies", Fields: []core.CategoryField{
				{Code: "wattage", Label: "Power", Kind: core.FieldNumber, Unit: "W", Export: true},
				{Code: "efficiency", Label: "Efficiency rating", Kind: core.FieldChoice, Options: opts(
					"80+ Titanium", "80+ Platinum", "80+ Gold", "80+ Silver",
					"80+ Bronze", "80+", "Unrated")},
				{Code: "modular", Label: "Modular cables", Kind: core.FieldBool},
			}},
		},
	}
}
